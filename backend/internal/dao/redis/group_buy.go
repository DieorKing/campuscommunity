// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为拼单模块的 Redis 库存初始化与查询，供 logic/group_buy.go 调用。
package redis

import (
	"fmt"
)

// groupBuyStockKey 拼单库存键：group_buy:{good_id}:stock。
// 键命名统一用业务主键 good_id（见 mvp §0.2），方便按拼单维度排查。
func groupBuyStockKey(goodID int64) string {
	return fmt.Sprintf("group_buy:%d:stock", goodID)
}

// groupBuyMembersKey 拼单成员键：group_buy:{good_id}:members（Set 集合）。
// 存已参与用户的 user_id，用于抢单去重（阶段4 使用）。
func groupBuyMembersKey(goodID int64) string {
	return fmt.Sprintf("group_buy:%d:members", goodID)
}

// InitGroupBuyStock 发布拼单时初始化 Redis 库存。
// stock 初始化为 max_members（= 成团上限名额），members 集合初始化为空。
// 语义：这两个键代表该拼单在 Redis 里的"份额池"，抢单阶段所有预扣都基于它。
// 无 TTL（持久键）：拼单生命周期内库存一直存在；订单超时/取消会 INCR 回补，
// 拼单过期后的清理属于延时任务职责，此处不设过期时间。
// 注意：两个 SET 非原子——若 DB 写入成功后这里失败，由 logic 层回滚（阶段3 讲解）。利用事务保持数据一致性
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
