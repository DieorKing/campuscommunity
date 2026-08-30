package model

import "time"

// ── 单据地图（拼单模块·接口）─────────────────────────────
// ParamCreateGroupBuy → 发布页的【填写单】（请求体 JSON）
// ParamListGroupBuy   → 列表页的【查询单】（请求 query：sort/page/page_size）
// GroupBuyItem        → 广场列表页的【卡片】（响应项）
// ListResult          → 所有列表接口的【分页外壳】（通用响应：list+total+page）
// GroupBuyDetail      → 详情页的【整页数据】（响应：商品+进度+人员+本人视角）
// MemberItem          → 详情页里的【一个头像格子】（响应子形状）
// （表结构单据在 group_buy.go：主体档案 + 签到簿）
// ─────────────────────────────────────────────────────────

// ParamCreateGroupBuy 【填写单·发布】发布页提交（POST /api/v1/group-buy，请求体 JSON）。
// binding 仅做格式硬约束（必填/范围/长度），业务规则（截止时间合法性、min ≤ max 等）在 logic 层校验。
// 字段注释中标注了对应的数据表字段，方便对照。
type ParamCreateGroupBuy struct {
	Title       string    `json:"title" binding:"required,max=100"`      // 商品名称（对应 group_buys.title）
	Description string    `json:"description" binding:"max=2000"`        // 描述（对应 group_buys.description，非必填）
	Price       float64   `json:"price" binding:"required,gt=0"`         // 单价（对应 group_buys.price，必须大于 0）
	MinMembers  int       `json:"min_members" binding:"required,gt=0"`   // 最低成团人数（必须 ≥ 1）
	MaxMembers  int       `json:"max_members" binding:"required,gt=0"`   // 成团上限/库存（必须 ≥ 1）
	ImageURL    string    `json:"image_url" binding:"omitempty,max=255"` // 商品图片链接（对应 group_buys.image_url，非必填）
	Deadline    time.Time `json:"deadline" binding:"required"`           // 拼单截止时间
}

// ParamListGroupBuy 【查询单】列表页提交（GET /api/v1/group-buy/list?sort=latest|hot&page=1&page_size=10，query 参数）。
// 全部可选（query 绑定零值即缺省），缺省值在 logic 层归一化，binding 不加 required——
// 「用户没传」和「传了 0」对分页参数语义相同，都该走默认值而非报参数错误。
type ParamListGroupBuy struct {
	Sort     string `form:"sort"`      // 排序方式：latest（默认，按创建时间倒序）/ hot（热度榜）
	Page     int    `form:"page"`      // 页码，从 1 开始，缺省 1
	PageSize int    `form:"page_size"` // 每页条数，缺省 10，上限 50
}

// GroupBuyItem 【卡片】广场列表页的响应项（JSON 响应，一卡片 = 一拼单的缩略信息）。
// 为什么单独定义响应 DTO 而不直接返回 model.GroupBuy：
//  1. 列表页不展示 description（最大 2000 字符），10 条/页直接返回会白传 20KB；
//  2. 前端展示需要「已参与人数」计算进度条，GroupBuy 里叫 current_members，
//     直接复用该字段名即可，但热榜项要额外带 hot_score——model 层没有该字段；
//  3. 分层约定：model 是 DB 映射（对内），DTO 是 API 形状（对外），两者解耦后互不牵连。
type GroupBuyItem struct {
	GoodID         ID             `json:"good_id"` // 业务主键，前端跳详情用
	Title          string         `json:"title"`
	Price          float64        `json:"price"`
	MinMembers     int            `json:"min_members"`
	MaxMembers     int            `json:"max_members"`
	CurrentMembers int            `json:"current_members"`
	ImageURL       string         `json:"image_url"`
	Status         GroupBuyStatus `json:"status"`    // recruiting/full/succeeded/failed/expired（枚举见 group_buy.go）
	Deadline       time.Time      `json:"deadline"`  // 截止时间（前端判断可否参与）
	HotScore       float64        `json:"hot_score"` // 热度分（仅 hot 排序有值，latest 为 0——统一字段省得前端判空）
	IsJoined       bool           `json:"is_joined"` // 本人是否已参与（登录后每项附加参与标记；未登录恒 false）
	CreatedAt      time.Time      `json:"created_at"`
}

// ListResult 【分页外壳】所有列表接口的通用响应形状（JSON 响应：list + total + page + page_size）。
// 复用子形状的嵌套示例（判据：多处复用 → 嵌）：List 字段装 []GroupBuyItem，
// 将来订单列表/通知列表同样套这个壳，只换 List 的元素类型。
// total 的语义因数据源而异（latest=精确 COUNT / hot=ZCARD 近似，见 logic 注释）。
type ListResult struct {
	List     any   `json:"list"`      // 当页数据（[]GroupBuyItem）
	Total    int64 `json:"total"`     // 总条数
	Page     int   `json:"page"`      // 当前页码（回显归一化后的值）
	PageSize int   `json:"page_size"` // 归一化后的页大小
}

// GroupBuyDetail 【整页数据】详情页的响应（GET /api/v1/group-buy/:id：
// 进页一次拿全——商品信息 + 进度 + 参与人员列表，之后轮询走轻量 /status）。
// 与列表项 GroupBuyItem 的三点差异（DTO 按「页面需要什么」裁剪，而非 model 照抄）：
//  1. 带 Description 全量描述——详情页要展示，列表页为控 payload 不带；
//  2. 带 Members 参与人员列表（含昵称/头像，跨表组装的结果）；
//  3. 带 IsJoined/IsPublisher 本人视角字段——前端据此渲染按钮态（未参与可抢/已参与禁用/发布者禁用）。
type GroupBuyDetail struct {
	GoodID         ID             `json:"good_id"`
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	Price          float64        `json:"price"`
	ImageURL       string         `json:"image_url"`
	MinMembers     int            `json:"min_members"`
	MaxMembers     int            `json:"max_members"`
	CurrentMembers int            `json:"current_members"`
	Deadline       time.Time      `json:"deadline"`
	Status         GroupBuyStatus `json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
	IsJoined       bool           `json:"is_joined"`    // 当前登录用户是否已参与（前端渲染「已参与」禁用态）
	IsPublisher    bool           `json:"is_publisher"` // 当前登录用户是否是发布者（前端渲染「我是发起人」禁用态）
	Members        []MemberItem   `json:"members"`      // 参与人员列表，按加入时间升序（不分页：≤ max_members 有界）
}

// MemberItem 【一个头像格子】详情页参与人员列表的子形状（JSON 响应项，嵌在 GroupBuyDetail.Members 里）。
// 昵称/头像不在 members 表（只有 user_id），由 logic 层 IN 批查 users 后组装——
// JoinedAt 直接用成员记录的 CreatedAt（加入时刻即创建时刻，建表时已决策不冗余 joined_at 列）。
type MemberItem struct {
	UserID   ID        `json:"user_id"`
	Nickname string    `json:"nickname"` // logic 层已做兜底：昵称为空回退 username，前端拿来即用
	Avatar   string    `json:"avatar"`
	JoinedAt time.Time `json:"joined_at"` // 加入时间（= group_buy_members.created_at）
}
