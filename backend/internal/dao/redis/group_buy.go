// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为拼单模块的 Redis 库存初始化与查询，供 logic/group_buy.go 调用。
package redis

import (
	"fmt"
)

// groupBuyStockKey 拼单库存键：group_buy:{good_id}:stock。
// 键命名统一用业务主键 good_id，方便按拼单维度排查。
func groupBuyStockKey(goodID int64) string {
	return fmt.Sprintf("group_buy:%d:stock", goodID)
}

// groupBuyMembersKey 拼单成员键：group_buy:{good_id}:members（Set 集合）。
// 存已参与用户的 user_id，用于抢单去重。
func groupBuyMembersKey(goodID int64) string {
	return fmt.Sprintf("group_buy:%d:members", goodID)
}

// InitGroupBuyStock 发布拼单时初始化 Redis 库存。
// stock 初始化为 max_members（= 成团上限名额），members 集合初始化为空。
// 语义：这两个键代表该拼单在 Redis 里的"份额池"，抢单阶段所有预扣都基于它。
// 无 TTL（持久键）：拼单生命周期内库存一直存在；订单超时/取消会 INCR 回补，
// 拼单过期后的清理属于延时任务职责，此处不设过期时间。
// 注意：两个 SET 非原子——若 DB 写入成功后这里失败，由 logic 层回滚，利用事务保持数据一致性
func InitGroupBuyStock(goodID int64, maxMembers int) error {
	// 初始化库存：SET group_buy:{good_id}:stock <max_members>
	if err := client.Set(groupBuyStockKey(goodID), maxMembers, 0).Err(); err != nil {
		return fmt.Errorf("redis: init group buy stock: %w", err)
	}
	// 初始化成员集合：DEL 清空历史残留后（理论不存在，防御性处理），再 SADD 为空即无操作。
	// 用 DEL 而非 SADD：确保集合从干净状态开始，避免上一轮测试残留污染。
	if err := client.Del(groupBuyMembersKey(goodID)).Err(); err != nil {
		return fmt.Errorf("redis: clear group buy members: %w", err)
	}
	return nil
}

// GetGroupBuyStock 查询拼单剩余名额（列表/详情展示，或抢单前预检）。
// 返回剩余数；键不存在（如未初始化）返回 0，由调用方结合业务判断。
func GetGroupBuyStock(goodID int64) (int64, error) {
	val, err := client.Get(groupBuyStockKey(goodID)).Int64()
	if err != nil {
		// 键不存在属于正常业务分支（该拼单无 Redis 份额），返回 0 而非错误
		if err == Nil {
			return 0, nil
		}
		return 0, fmt.Errorf("redis: get group buy stock: %w", err)
	}
	return val, nil
}

// ReleaseGroupBuySlot 释放拼单名额（用户取消订单时调用：仅 INCR stock）。
// 与预扣（DECR）互为逆操作；让出的名额退回池子供【其他用户】预扣。
// 时序约定：必须在 DB 取消事务【提交成功后】调用——Redis 是派生数据，
// 真值源（订单状态）先行；若反序，DB 取消失败而 Redis 已放名额 = 超卖窗口。
//
// 不做 SREM members（判重语义收口）：取消后本人【不可再抢同一拼单】——
// DB orders 表 (user_id, good_id) 唯一索引 uk_user_good 覆盖 cancelled
// 历史参与，重抢必撞 Duplicate entry（消费者按幂等跳过，建不出新订单）。
// Redis 与 DB 两层判重必须保持同一语义：若此处 SREM 放行而 DB 拦死，
// 会出现「grabbed=true 却永远无订单」的静默失败（终测实踩）。
// 取消的参与记录以 cancelled 订单形式保留（历史事实不可篡改），
// 用户想再参与需选择其他拼单。
//
// 失败语义 best-effort：调用方记日志观察，修复靠人工/补偿任务。
func ReleaseGroupBuySlot(goodID int64) error {
	// 库存 +1：让出的名额退回池子，可被后续抢单者预扣
	if err := client.Incr(groupBuyStockKey(goodID)).Err(); err != nil {
		return fmt.Errorf("redis: release slot incr stock: %w", err)
	}
	return nil
}
