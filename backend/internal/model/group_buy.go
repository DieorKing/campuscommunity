package model

import "time"

// ── 单据地图（拼单模块·表）───────────────────────────────
// GroupBuy       → group_buys 表的【拼单主体档案】：商品+进度+状态（DB 行）
// GroupBuyMember → group_buy_members 表的【签到簿】：谁在何时参加了哪个团（DB 行）
// GroupBuyStatus → 拼单生命周期状态枚举（表内字段值域）
// 拼单模块的接口单据（发布/列表/详情等六张单）在 param_group_buy.go
// ─────────────────────────────────────────────────────────

// GroupBuyStatus 拼单状态枚举。
// 用 typed string 而非裸 string：编译期防止拼错状态值（如 "succceded"），
// 且 IDE 可自动补全，重构时 grep 一发命中。
type GroupBuyStatus string

const (
	GroupBuyRecruiting GroupBuyStatus = "recruiting" // 拼单中（可抢单）
	GroupBuyFull       GroupBuyStatus = "full"       // 已满员（不可再抢，待截止统一判定）
	GroupBuySucceeded  GroupBuyStatus = "succeeded"  // 已成团（达 min_members）
	GroupBuyFailed     GroupBuyStatus = "failed"     // 截止时未达最低人数
	GroupBuyExpired    GroupBuyStatus = "expired"    // 已过期
)

// GroupBuy 【拼单主体档案】group_buys 表行：商品信息 + 成团进度 + 状态（DB 映射，model 层）。
// 库存语义：max_members 是名额上限（= 库存），current_members 是已占名额，
// Redis 预扣的 stock = max_members - current_members（发布时初始化）。
type GroupBuy struct {
	BaseModel
	GoodID         int64          `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"good_id"`
	PublisherID    int64          `gorm:"index;not null;comment:发布者(引用users.user_id)" json:"publisher_id"`
	Title          string         `gorm:"type:varchar(100);not null;comment:商品名称" json:"title"`
	Description    string         `gorm:"type:text;comment:描述" json:"description"`
	Price          float64        `gorm:"type:decimal(10,2);not null;comment:单价" json:"price"`
	ImageURL       string         `gorm:"type:varchar(255);comment:商品图片链接" json:"image_url"`
	MinMembers     int            `gorm:"not null;comment:最低成团人数" json:"min_members"`
	MaxMembers     int            `gorm:"not null;comment:成团上限(即库存)" json:"max_members"`
	CurrentMembers int            `gorm:"default:0;comment:当前参与人数" json:"current_members"`
	Deadline       time.Time      `gorm:"not null;comment:拼单截止时间" json:"deadline"`
	Status         GroupBuyStatus `gorm:"type:varchar(20);index;not null;default:recruiting;comment:拼单状态" json:"status"`
}

// GroupBuyMember 【签到簿】group_buy_members 表行：一行 = 某用户参加了某拼单（DB 映射）。
// 支撑详情页「参与人员」与去重。
// 唯一约束 (good_id, user_id) 是防重复参与的 DB 层兜底——
// 即使 Redis 去重失效（如 Redis 重启丢数据），DB 唯一索引也能拦住重复插入。
// joined_at 字段已省略：成员记录创建时刻即加入时刻，BaseModel.CreatedAt 语义相同，不冗余存两列。
type GroupBuyMember struct {
	BaseModel
	MemberID int64 `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"member_id"`
	// uniqueIndex:uk_good_user 两字段同名索引 = 复合唯一索引 (good_id, user_id)
	GoodID int64 `gorm:"uniqueIndex:uk_good_user;not null;comment:关联拼单(引用group_buys.good_id)" json:"good_id"`
	UserID int64 `gorm:"uniqueIndex:uk_good_user;not null;comment:参与用户(引用users.user_id)" json:"user_id"`
}
