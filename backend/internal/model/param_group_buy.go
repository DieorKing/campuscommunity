package model

import "time"

// ParamCreateGroupBuy 发布拼单请求参数（POST /api/v1/group-buy）。
// binding 仅做格式硬约束（必填/范围/长度），业务规则（截止时间合法性、min ≤ max 等）在 logic 层校验。
// 字段注释中标注了对应的数据表字段，方便对照。
type ParamCreateGroupBuy struct {
	Title       string    `json:"title" binding:"required,max=100"`           // 商品名称（对应 group_buys.title）
	Description string    `json:"description" binding:"max=2000"`             // 描述（对应 group_buys.description，非必填）
	Price       float64   `json:"price" binding:"required,gt=0"`              // 单价（对应 group_buys.price，必须大于 0）
	MinMembers  int       `json:"min_members" binding:"required,gt=0"`        // 最低成团人数（必须 ≥ 1）
	MaxMembers  int       `json:"max_members" binding:"required,gt=0"`        // 成团上限/库存（必须 ≥ 1）
	ImageURL    string    `json:"image_url" binding:"omitempty,max=255"`      // 商品图片链接（对应 group_buys.image_url，非必填）
	Deadline    time.Time `json:"deadline" binding:"required"`                // 拼单截止时间
}