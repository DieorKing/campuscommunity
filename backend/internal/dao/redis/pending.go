// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为 pending→success 两阶段状态标记的 pending 侧操作：
// 建单消息的「本地消息表」（Transactional Outbox 思想）——
// 预扣时 Lua 内原子落标记，消费者建单成功后移除，
// 滞留超时 = 消息丢失，由对账扫描器重发修复。
package redis

import (
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis"
)

// pendingGrabKey pending 标记键：pending:grab（全局单键 ZSet）。
// member = "{good_id}:{user_id}"，score = 消息发布时刻 Unix 秒。
// 全局单键而非 per-good 键：对账扫描器一次 ZRANGEBYSCORE 捞全平台
// 滞留标记，无需遍历拼单维度（与 delay:order:close 同一设计取舍）。
// Cluster 边界：与 group_buy:{good_id}:* 可能跨 slot，
// grabLua 注释中有。
func pendingGrabKey() string {
	return "pending:grab"
}

// pendingMember 标记成员格式：{good_id}:{user_id}。
// 两段雪花 ID 用":" 分隔——解析侧 strings.SplitN 两段还原，
// 雪花 ID 本身不含":"，不会有歧义。
func pendingMember(goodID, userID int64) string {
	return fmt.Sprintf("%d:%d", goodID, userID)
}

// ListExpiredPendingGrabs 捞出滞留超时的 pending 标记（对账扫描器每轮调用）。
// ZRANGEBYSCORE pending:grab -inf <(now-timeout)：「读不删」——
// 移除/续期由处理方按结果执行，崩溃安全与关单队列同一设计：
// 读了没处理完进程挂了，标记还在，下轮重捞。
// timeout 由调用方传入（logic 层常量，30s，见 pending.go 注释）。
func ListExpiredPendingGrabs(now time.Time, timeout time.Duration) ([]string, error) {
	ids, err := client.ZRangeByScore(pendingGrabKey(), redis.ZRangeBy{
		Min:   "-inf",
		Max:   fmt.Sprintf("(%d", now.Add(-timeout).Unix()),
		Count: 1000,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: list expired pending grabs: %w", err)
	}
	return ids, nil
}

// RemoveGrabPending 移除 pending 标记（消费者建单成功/确定性失败后调用）。
// ZREM 幂等：重复移除 rows=0 无害。残留标记（ZREM 失败）下一轮被
// 对账扫描器重捞 → 重发消息 → 消费者撞唯一索引 ack → 再 ZREM——
// 自愈闭环。
func RemoveGrabPending(goodID, userID int64) error {
	if err := client.ZRem(pendingGrabKey(), pendingMember(goodID, userID)).Err(); err != nil {
		return fmt.Errorf("redis: remove grab pending: %w", err)
	}
	return nil
}

// RenewGrabPending 续期标记（对账扫描器重发消息后调用）：
// score 重置为 now，给消费者一个完整的超时窗口消化重发的消息——
// 不续期则下一轮（10s 后）标记仍超时，会重复重发（幂等无害但浪费）。
// ZADD 对已存在 member 是覆盖 score 语义，天然幂等。
func RenewGrabPending(goodID, userID int64, now time.Time) error {
	err := client.ZAdd(pendingGrabKey(), redis.Z{
		Score:  float64(now.Unix()),
		Member: pendingMember(goodID, userID),
	}).Err()
	if err != nil {
		return fmt.Errorf("redis: renew grab pending: %w", err)
	}
	return nil
}

// ParsePendingMember 解析标记成员 "{good_id}:{user_id}" 为两段 ID。
// 消费侧（对账扫描器）用：重发消息需要 good_id 与 user_id 两个参数。
func ParsePendingMember(member string) (goodID, userID int64, err error) {
	parts := splitFirst(member, ':')
	goodID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("redis: parse pending member %q: %w", member, err)
	}
	userID, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("redis: parse pending member %q: %w", member, err)
	}
	return goodID, userID, nil
}

// splitFirst 按 sep 切一刀取前后两段（成员格式固定两段，不用 Split 全切）。
func splitFirst(s string, sep byte) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}
