package model

// NotificationType 通知大类：拼单 or 订单。
type NotificationType string

const (
	NotifyGroupBuy NotificationType = "group_buy" // 拼单类通知
	NotifyOrder    NotificationType = "order"     // 订单类通知
)

// NotificationCategory 通知细类（同一 type 下的具体事件）。
// 与 mvp §6.3 category 枚举一一对应。
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

// Notification 应用内通知表（MVP 唯一通知渠道：前端轮询本表拉取）。
// 消息流转：业务事件 → RabbitMQ → 消费者写本表 → 前端刷新拉取（阶段7 实现）。
type Notification struct {
	BaseModel
	NotificationID int64                `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"notification_id"`
	UserID         int64                `gorm:"index;not null;comment:接收人(引用users.user_id)" json:"user_id"`
	Type           NotificationType     `gorm:"type:varchar(30);not null;comment:通知大类group_buy/order" json:"type"`
	Category       NotificationCategory `gorm:"type:varchar(30);not null;comment:通知细类" json:"category"`
	Title          string               `gorm:"type:varchar(100);comment:标题" json:"title"`
	Content        string               `gorm:"type:varchar(500);comment:正文" json:"content"`
	// RefID 多态关联：type=group_buy 时存 good_id；type=order 时存 order_id。
	// MVP 不拆两张表，用 type 区分语义即可。
	RefID  int64 `gorm:"comment:关联good_id或order_id" json:"ref_id"`
	IsRead bool  `gorm:"default:false;comment:是否已读" json:"is_read"`
}
