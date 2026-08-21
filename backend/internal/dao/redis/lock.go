// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为抢单流程的分布式锁（契约 mvp §4.3 step2 获取 / step5 释放）。
package redis

import (
	"fmt"
	"strconv"
	"time"

	"campuscommunity/pkg/utils/snowflake"
)

// lockKey 抢单分布式锁键：group_buy:{good_id}:lock。
// 锁粒度是拼单级（按 good_id 分锁）而非全局：不同拼单的抢单互不阻塞，
// 保证并发吞吐——全局锁会把所有拼单的抢单请求串成一条队。
// 键名含 good_id，Redis Cluster 下与 stock/members 键天然同 slot。
func lockKey(goodID int64) string {
	return fmt.Sprintf("group_buy:%d:lock", goodID)
}

// lockTTL 锁自动过期时间 3 秒（契约 mvp §4.3）。
// 为什么必须有 TTL：持锁进程崩溃后无人主动释放，锁永不过期 = 该拼单的
// 所有后续抢单永久阻塞（死锁）。TTL 是崩溃场景的自动兜底——宁可 3 秒后
// 放别人进来，也不能让一把死锁挂死整个拼单。
// 取 3 秒的依据：临界区内只有一次 Lua 预扣 + 一次 MQ 投递，正常耗时毫秒级，
// 3 秒含充足余量。已知局限：业务真超 3 秒锁会被他人获取（无看门狗续期），
// 生产环境用 Redisson watchdog 解决，本项目接受该限制。
const lockTTL = 3 * time.Second

// releaseLockLua 释放锁的 Lua 脚本：GET 比对 token 一致才 DEL。
// 为什么必须 Lua（不能 GET + DEL 两条命令）：比对与删除之间存在间隙——
// GET 比对通过后锁恰好过期、B 获得锁，随后的 DEL 删掉的是 B 的锁（误删）。
// Lua 使「比对+删除」原子执行，间隙消失。
// 返回 1 = 删除成功（锁确属本持有者）；0 = 比对失败（锁已过期或易主），未删除。
const releaseLockLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0
`

// AcquireLock 获取拼单级分布式锁（SET key token NX EX 3s，契约 mvp §4.3 step2）。
// token 是锁持有者凭证：释放时必须比对一致才允许删除——防止
// 「A 的锁过期 → B 获得锁 → A 恢复后 DEL」误删 B 的锁。
// token 用雪花 ID 生成（教科书用 UUID；任何全局唯一的不透明值都可充当，
// 复用已有雪花算法省去 UUID 依赖）。
// 返回值：(token, true, nil) 拿锁成功；("", false, nil) 锁被他人持有
// （快速失败，属正常竞争结果而非错误）；err 仅 Redis 故障时非 nil。
func AcquireLock(goodID int64) (string, bool, error) {
	// 每次拿锁生成新 token：本持有者的唯一凭证
	token := strconv.FormatInt(snowflake.GenID(), 10)
	// SET key token NX EX 3：NX = 键不存在才能设置（互斥），EX = 带过期（防死锁）。
	// NX 与 EX 必须在同一条命令内完成：若拆成 SETNX + EXPIRE 两步，SETNX 成功后
	// 进程崩溃、EXPIRE 未执行，锁将永不过期 = 死锁。
	ok, err := client.SetNX(lockKey(goodID), token, lockTTL).Result()
	if err != nil {
		return "", false, fmt.Errorf("redis: acquire grab lock: %w", err)
	}
	return token, ok, nil
}

// ReleaseLock 释放拼单级分布式锁（Lua 比对 token 后原子删除，契约 mvp §4.3 step5）。
// 返回 true = 锁由本持有者正常释放；false = 锁已过期或已被他人获取（未删除）。
// false 不视为错误：锁过期是 TTL 机制的正常行为，调用方记日志观察即可。
func ReleaseLock(goodID int64, token string) (bool, error) {
	result, err := client.Eval(releaseLockLua, []string{lockKey(goodID)}, token).Result()
	if err != nil {
		return false, fmt.Errorf("redis: release grab lock: %w", err)
	}
	// Eval 返回 interface{}：数字回复为 int64（DEL 的返回值 1(成功删除)/0(未删除)）。
	// 双返回值断言，不裸断言——类型不符说明协议被破坏，显式报错。
	n, ok := result.(int64)
	if !ok {
		return false, fmt.Errorf("redis: release grab lock: unexpected reply type %T", result)
	}
	return n == 1, nil
}
