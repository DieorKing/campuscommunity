// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 本文件为订单模块的 MySQL 数据访问（阶段4 仅 /status 轮询查询；
// 建单写入在阶段5 由 MQ 消费者实现）。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"fmt"

	"gorm.io/gorm"
)

// GetOrderByUserAndGood 按用户 + 拼单查询订单（/status 轮询：抢到后查自己的订单号）。
// 返回约定同 GetUserByUsername：查不到返回 (nil, nil)——「订单尚未生成」是
// 正常业务分支（MQ 消费者还在处理，轮询的本义就是等它），由 logic 层
// 翻译为「受理中」；仅 DB 故障返回 err。
// 走 uk_user_good 复合唯一索引等值查询，单行返回。
// 幂等关联：(user_id, good_id) 唯一索引是消费端防重复建单的物理防线——
// 重复消息 INSERT 撞索引失败，消费者识别后直接 ack 跳过（阶段5 实现）。
func GetOrderByUserAndGood(userID, goodID int64) (*model.Order, error) {
	var order model.Order
	err := mysql.GetDB().Where("user_id = ? AND good_id = ?", userID, goodID).First(&order).Error
	// ErrRecordNotFound 是业务上的「还没建单」，就地消化，不让 logic 层 import gorm
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get order by user and good: %w", err)
	}
	return &order, nil
}
