// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 本文件为通知模块的 MySQL 数据访问：消费端幂等落库 + 用户通知列表 + 标记已读。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// ErrNotificationDuplicate 通知重复落库哨兵：撞 uk_user_category_ref 复合唯一
// 索引（同 user+category+ref 的事件已通知过）。语义与订单的 ErrDuplicateEntry
// 平行：消费端识别后静默 ack——重复通知是 at-least-once 的正常代价，不是错误
// （量级大，连 Info 日志都不打，防止刷屏）。
var ErrNotificationDuplicate = errors.New("通知已存在")

// CreateNotification 通知消费者落库（INSERT，幂等入口在唯一索引）。
// 不做前置 SELECT 判重——「查」与「插」之间的间隙是 check-then-act 竞态，
// 唯一索引才是物理防线（与建单 INSERT 同一手法）。
// 返回 ErrNotificationDuplicate = 重复事件已通知过，调用方静默 ack。
func CreateNotification(n *model.Notification) error {
	if err := mysql.GetDB().Create(n).Error; err != nil {
		// 1062 判别：errors.As 沿 %w 包装链穿透 GORM/驱动找到服务器错误包；
		// 连接断开时服务器无机会返回错误包，自动落入暂时性分支走 nack
		var me *mysqldriver.MySQLError
		if errors.As(err, &me) && me.Number == mysqlDupEntryErrNum {
			return ErrNotificationDuplicate
		}
		return fmt.Errorf("dao: create notification: %w", err)
	}
	return nil
}

// ListNotificationsByUserPage 按用户分页查询通知（新在前）。
// 不在 SQL 过滤 is_read——前端「全部/未读」两个 Tab 共用本查询，
// 前端本地过滤 30s 轮询窗口内的少量数据（每轮 ≤ page_size 条）；
// 服务端过滤需两个接口两套参数，收益小于复杂度。
// 按 id 倒序 = 创建时间倒序（雪花趋势递增 + 自增 id 双保险）。
func ListNotificationsByUserPage(userID int64, page, pageSize int) ([]model.Notification, int64, error) {
	var (
		list  []model.Notification
		total int64
	)
	db := mysql.GetDB().Model(&model.Notification{}).Where("user_id = ?", userID)
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("dao: count notifications: %w", err)
	}
	if total == 0 {
		return list, 0, nil
	}
	offset := (page - 1) * pageSize
	if err := db.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("dao: list notifications page: %w", err)
	}
	return list, total, nil
}

// GetNotificationByID 按业务主键查单条通知（标记已读的前置查询）。
// 返回约定同本包其他 Get：(nil, nil) = 不存在（正常业务分支，logic
// 翻译 ErrNotificationNotExist），仅 DB 故障返回 err。
// 走 notification_id 唯一索引等值查询。
func GetNotificationByID(notificationID int64) (*model.Notification, error) {
	var n model.Notification
	err := mysql.GetDB().Where("notification_id = ?", notificationID).First(&n).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get notification by id: %w", err)
	}
	return &n, nil
}

// MarkNotificationRead 标记单条通知已读（条件 UPDATE：WHERE is_read = false）。
// 幂等性：已读行重放 rows=0 落空——「已读」事实只被记录一次；
// WHERE 同时限定 user_id 防越权标记他人通知（资源所有权校验压进单条语句）。
// 返回 (true, nil) = 标记成功；(false, nil) = 不存在/非本人/已读（统一落空，
// 由调用方前置查询区分语义）；err 仅 DB 故障。
func MarkNotificationRead(notificationID, userID int64) (bool, error) {
	r := mysql.GetDB().Model(&model.Notification{}).
		Where("notification_id = ? AND user_id = ? AND is_read = ?",
			notificationID, userID, false).
		Update("is_read", true)
	if r.Error != nil {
		return false, fmt.Errorf("dao: mark notification read: %w", r.Error)
	}
	return r.RowsAffected == 1, nil
}

// CountUnreadNotifications 用户未读数（前端角标轮询用，轻量 COUNT 走 user_id 索引）。
func CountUnreadNotifications(userID int64) (int64, error) {
	var n int64
	if err := mysql.GetDB().Model(&model.Notification{}).
		Where("user_id = ? AND is_read = ?", userID, false).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("dao: count unread notifications: %w", err)
	}
	return n, nil
}
