package model

import "time"

// OrderStatus 订单状态枚举。
// 状态机（mvp §4.4）：
//
//	pending_pay → paid → completed          （正常流）
//	pending_pay → cancelled                 （用户主动取消，仅待支付可取消）
//	pending_pay → closed                    （30min 超时由延时任务关闭）
type OrderStatus string

const (
	OrderPendingPay OrderStatus = "pending_pay" // 待支付
	OrderPaid       OrderStatus = "paid"        // 已支付
	OrderCancelled  OrderStatus = "cancelled"   // 用户已取消
	OrderCompleted  OrderStatus = "completed"   // 已完成
	OrderClosed     OrderStatus = "closed"      // 超时关闭
)

// Order 订单表：抢单成功后自动创建（阶段4 抢单流程 step4）。
// order_id 即订单号（雪花生成），对外暴露；amount/address 是下单时刻快照，
// 后续拼单改价 / 用户改地址均不影响历史订单。
type Order struct {
	BaseModel
	OrderID int64   `gorm:"uniqueIndex;not null;comment:业务主键(雪花,即订单号)" json:"order_id"`
	// uk_user_good 复合唯一 = 防重复下单 DB 兜底（与 members 表的去重双保险）；
	// 单独 index 用于「按用户查订单列表」的高频查询路径。
	UserID   int64       `gorm:"uniqueIndex:uk_user_good;index;not null;comment:下单用户(引用users.user_id)" json:"user_id"`
	GoodID   int64       `gorm:"uniqueIndex:uk_user_good;index;not null;comment:关联拼单(引用group_buys.good_id)" json:"good_id"`
	Amount   float64     `gorm:"type:decimal(10,2);not null;comment:金额(拼单price快照)" json:"amount"`
	Address  string      `gorm:"type:varchar(255);comment:下单时地址快照" json:"address"`
	Status   OrderStatus `gorm:"type:varchar(20);index;not null;default:pending_pay;comment:订单状态" json:"status"`
	// PaidAt/ClosedAt 用指针：未支付/未关闭时为 NULL（JSON null）。
	// 若用值类型 time.Time，零值会序列化成 "0001-01-01T00:00:00Z"，语义错误且丑。
	PaidAt   *time.Time `gorm:"comment:支付时间(可空)" json:"paid_at"`
	ClosedAt *time.Time `gorm:"comment:关闭时间(可空)" json:"closed_at"`
}
