// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 本文件为拼单模块的 MySQL 数据访问，供 logic/group_buy.go 调用。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"fmt"
)

// CreateGroupBuy 新增一条拼单记录（发布拼单时调用）。
// 入参是已由 logic 层填充好的 *model.GroupBuy：
//   - GoodID：雪花算法生成，logic 层赋值
//   - PublisherID：当前登录用户 user_id，logic 层赋值
//   - Status：默认 recruiting、CurrentMembers 默认 0（由 GORM 默认值处理）
//
// 返回 (good_id, err)：good_id 供 HTTP 层回显给前端；插入失败返回包装错误。
// DAO 不做任何业务校验（如 title 长度、deadline 合法性），那些属于 logic 层职责。
func CreateGroupBuy(g *model.GroupBuy) (int64, error) {
	if err := mysql.GetDB().Create(g).Error; err != nil {
		return 0, fmt.Errorf("dao: create group buy: %w", err)
	}
	// 创建成功后 g.ID（内部自增主键）与 g.GoodID（业务主键）都被 GORM 回填，
	// 返回业务主键给上层用于对外暴露。
	return g.GoodID, nil
}

// DeleteGroupBuy 按业务主键删除拼单记录（发布流程中 Redis 初始化失败的补偿回滚）。
// 为什么需要：DB 写成功、Redis 初始化失败时，若不删除 DB 记录，
// 会出现「DB 有拼单、Redis 无库存」的不一致态，后续抢单无法进行。
// 用物理删除（Unscoped 不生效，表无软删字段）直接删行，简单可靠。
// 硬删除需谨慎：本函数仅用于创建后立即回滚（无子记录），非业务删除场景。
func DeleteGroupBuy(goodID int64) error {
	result := mysql.GetDB().Where("good_id = ?", goodID).Delete(&model.GroupBuy{})
	if result.Error != nil {
		return fmt.Errorf("dao: delete group buy: %w", result.Error)
	}
	return nil
}
