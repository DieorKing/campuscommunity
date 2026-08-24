package model

// ── 单据地图（订单模块·接口单据）───────────────────────
// ParamListOrder  → 订单列表页的查询单（GET query 绑定）
// OrderListResult → 订单列表页的响应单（Total + 分页数据）
// ─────────────────────────────────────────────────────────

// ParamListOrder 【查询单】我的订单列表提交（GET /api/v1/order/list?status=&page=&page_size=）。
// 全部可选：status 空 = 全部状态；分页零值由 logic 层归一化（与 ParamListGroupBuy 同规约，
// binding 不加 required——「没传」和「传 0」都走默认值）。
type ParamListOrder struct {
	Status   string `form:"status"`    // 状态过滤：pending_pay/paid/cancelled/completed/closed，空=全部
	Page     int    `form:"page"`      // 页码，从 1 开始，缺省 1
	PageSize int    `form:"page_size"` // 每页条数，缺省 10，上限 50
}

// OrderListResult 【响应单】订单列表页数据（total 供前端显示总数/翻页终止判断）。
// List 直接复用 model.Order（订单含 address/amount 快照，是本人数据，
// 无脱敏需求——与拼单列表的 GroupBuyItem DTO 场景不同）。
type OrderListResult struct {
	Total int64   `json:"total"` // 该用户的订单总数（按 status 过滤后）
	List  []Order `json:"list"`  // 当前页订单（按建单时间倒序）
}
