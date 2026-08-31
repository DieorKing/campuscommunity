// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为抢单 Lua 原子预扣与参与标记查询（/status 轮询用）。
package redis

import (
	"fmt"
	"strconv"
	"time"
)

// grabLua 抢单预扣脚本：SISMEMBER 查重 → DECR 扣减 → SADD 登记 →
// ZADD pending 标记，四步原子执行。
//
// 为什么必须 Lua（三条命令分开发会发生的灾难，check-then-act 竞态）：
// 库存剩 1 时 A、B 同时抢——两个请求都通过 SISMEMBER 查重（谁都还没 SADD），
// 都执行 DECR，库存 1→0→-1，两人都进团 = 超卖。同一用户手抖双击同理：
// 一个用户占掉两个名额。「查」与「改」之间的间隙内，查重结果已经过期。
// Redis 单线程执行 Lua 期间不插入任何其他命令，间隙被封死。
//
// pending 标记（pending→success 两阶段状态标记的 pending 半边）：
// 预扣成功即在全局 ZSet pending:grab 落一条「{good_id}:{user_id}」，
// score = 发布时刻 Unix 秒。消费者建单成功（或确定性失败）后 ZREM。
// 标记与预扣同脚本原子写入：预扣成功但 MQ 投递失败/消息中途丢失时，
// 标记滞留超时即由对账扫描器重发消息——「业务成功了消息一定发出去」
// 的 Transactional Outbox 思想，pending ZSet 即本地消息表。
//
// 工程红线：脚本仅 5 个 O(1) 命令，微秒级执行。业务逻辑永不进脚本
// （业务落地归 MQ 消费者）；禁止 KEYS *、大集合遍历、无界循环。
// DECR 后的 INCR 回滚必须在脚本内部完成：若放到应用层做，Lua 之外
// 又产生间隙，竞态从后门回来。
//
// 键的 slot 一致性：KEYS[1]/KEYS[2] 按 good_id 构造，Redis Cluster 下
// 天然同 slot；KEYS[3]（pending:grab）是全局单键，Cluster 部署时与
// 前两者可能跨 slot——单实例部署无影响，Cluster 化时需将 pending 键
// 改为 hash tag 同 slot 或拆 per-good 键（已知边界）。
//
// 返回值协议（logic 层翻译为业务哨兵错误）：
//
//	DUPLICATE  该用户已参与（幂等防线一：重复请求在此终结，库存未动）
//	SOLD_OUT   库存不足（脚本内已 INCR 回滚，stock 恢复原值）
//	OK         预扣成功（库存 -1 + 成员登记 + pending 标记，一步到位）
const grabLua = `
if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then
	return 'DUPLICATE'
end
local stock = redis.call('DECR', KEYS[2])
if stock < 0 then
	redis.call('INCR', KEYS[2])
	return 'SOLD_OUT'
end
redis.call('SADD', KEYS[1], ARGV[1])
redis.call('ZADD', KEYS[3], ARGV[2], ARGV[3])
return 'OK'
`

// Lua 脚本返回值常量：DAO 与 logic 之间的字符串协议，logic 按此分支翻译业务语义。
const (
	GrabLuaOK        = "OK"        // 预扣成功
	GrabLuaSoldOut   = "SOLD_OUT"  // 库存不足
	GrabLuaDuplicate = "DUPLICATE" // 重复参与
)

// ExecGrabLua 执行抢单预扣脚本（在分布式锁内调用）。
// KEYS[1] = members 集合键，KEYS[2] = stock 库存键，KEYS[3] = pending 标记键，
// ARGV[1] = user_id，ARGV[2] = 当前 Unix 秒（pending score），ARGV[3] = pending 成员。
// 返回三个常量之一；err 仅在 Redis 故障时非 nil。
func ExecGrabLua(goodID, userID int64) (string, error) {
	// user_id 统一以 string 形态进 Redis：Lua 的 ARGV 恒为字符串，
	// SADD 登记与 SISMEMBER 查重必须用同一形态，成员比较才成立
	// （Redis 一切皆字符串，写入读取两端类型口径必须一致）。
	result, err := client.Eval(grabLua,
		[]string{groupBuyMembersKey(goodID), groupBuyStockKey(goodID), pendingGrabKey()},
		strconv.FormatInt(userID, 10),
		strconv.FormatInt(time.Now().Unix(), 10),
		pendingMember(goodID, userID),
	).Result()
	if err != nil {
		return "", fmt.Errorf("redis: exec grab lua: %w", err)
	}
	// Eval 返回 interface{}：字符串回复断言为 string，双返回值形式不裸断言
	s, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("redis: exec grab lua: unexpected reply type %T", result)
	}
	return s, nil
}

// IsGroupBuyMember 查询用户是否已参与拼单（/status 轮询接口用）。
// members 集合的 SISMEMBER O(1) 查询。
// 与脚本内查重的区别：本函数是普通读（无并发风险），抢单路径的查重
// 必须在 Lua 内原子完成，不能用本函数替代（check-then-act 竞态）。
// 成员形态与 ExecGrabLua 一致：string 形态的 user_id。
func IsGroupBuyMember(goodID, userID int64) (bool, error) {
	ok, err := client.SIsMember(groupBuyMembersKey(goodID), strconv.FormatInt(userID, 10)).Result()
	if err != nil {
		return false, fmt.Errorf("redis: check group buy member: %w", err)
	}
	return ok, nil
}
