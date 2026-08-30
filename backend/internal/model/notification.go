package model

// ── 单据地图（通知模块·表）───────────────────────────────
// Notification          → notifications 表的【一封信】：发给某用户的站内信（DB 行）
// NotificationType      → 通知大类枚举（group_buy / order）
// NotificationCategory  → 通知细类枚举（成团/失败/待支付…）
// 通知模块的接口单据（通知列表参数/响应）见 param_notification.go
// ─────────────────────────────────────────────────────────

// NotificationType 通知大类：拼单 or 订单。
type NotificationType string

const (
	NotifyGroupBuy NotificationType = "group_buy" // 拼单类通知
	NotifyOrder    NotificationType = "order"     // 订单类通知
)

// NotificationCategory 通知细类（同一 type 下的具体事件）。
// 与通知 category 枚举一一对应。
type NotificationCategory string

const (
	// 拼单类
	CategorySucceeded NotificationCategory = "succeeded" // 已成团
	CategoryFailed    NotificationCategory = "failed"    // 拼单失败
	CategoryExpiring  NotificationCategory = "expiring"  // 即将结束

	// 订单类
	CategoryPendingPay NotificationCategory = "pending_pay" // 待支付
	CategoryPaid       NotificationCategory = "paid"        // 已支付
	CategoryCancelled  NotificationCategory = "cancelled"   // 已取消
	CategoryCompleted  NotificationCategory = "completed"   // 已完成
	CategoryClosed     NotificationCategory = "closed"      // 已关闭
)

// Notification 【一封信】notifications 表行：发给某用户的一条站内信（DB 映射，model 层）。
// 该demo 唯一通知渠道：前端 30s 轮询本表拉取。
// 消息流转：业务事件 → RabbitMQ → 消费者写本表 → 前端刷新拉取（通知模块实现）。
type Notification struct {
	BaseModel
	NotificationID ID `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"notification_id"`
	// uk_user_category_ref 复合唯一 = 消费端幂等的物理防线：同一用户对同一
	// 事件（category+ref_id）只落一条通知——MQ at-least-once 重复投递撞索引
	// 被拒，消费端静默 ack。与订单 uk_user_good 同一手法。
	UserID   ID                   `gorm:"uniqueIndex:uk_user_category_ref;index;not null;comment:接收人(引用users.user_id)" json:"user_id"`
	Type     NotificationType     `gorm:"type:varchar(30);not null;comment:通知大类group_buy/order" json:"type"`
	Category NotificationCategory `gorm:"type:varchar(30);not null;uniqueIndex:uk_user_category_ref;comment:通知细类" json:"category"`
	Title    string               `gorm:"type:varchar(100);comment:标题" json:"title"`
	Content  string               `gorm:"type:varchar(500);comment:正文" json:"content"`
	// RefID 多态关联：type=group_buy 时存 good_id；type=order 时存 order_id。
	// 不拆两张表，用 type 区分语义。
	RefID  ID   `gorm:"uniqueIndex:uk_user_category_ref;comment:关联good_id或order_id" json:"ref_id"`
	IsRead bool `gorm:"default:false;comment:是否已读" json:"is_read"`
}
