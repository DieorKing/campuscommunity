package model

// ── 单据地图（通知模块·接口单据）───────────────────────
// ParamListNotification     → 通知列表页的查询单（GET query 绑定）
// NotificationListResult    → 通知列表页的响应单（Total + 分页数据 + 未读数）
// ─────────────────────────────────────────────────────────

// ParamListNotification 【查询单】通知列表提交（GET /api/v1/notification/list?page=&page_size=）。
// 全部可选：分页零值由 logic 层归一化（与 ParamListOrder 同规约）。
// 不设 is_read 过滤参数：前端「全部/未读」两个 Tab 共用本接口，
// 前端本地过滤 30s 轮询窗口内的少量数据——服务端过滤需两套参数两套
// 查询，收益小于复杂度（见 dao.ListNotificationsByUserPage 注释）。
type ParamListNotification struct {
	Page     int `form:"page"`      // 页码，从 1 开始，缺省 1
	PageSize int `form:"page_size"` // 每页条数，缺省 10，上限 50
}

// NotificationListResult 【响应单】通知列表页数据。
// Unread 内嵌在列表响应里：前端 30s 轮询本接口一次，列表+角标同时刷新，
// 省一次独立的 unread_count 轮询请求（读合并——轮询场景请求数就是成本）。
type NotificationListResult struct {
	Total int64          `json:"total"`  // 该用户的通知总数（前端翻页终止判断）
	Unread int64         `json:"unread"` // 未读数（角标显示，随列表一次返回）
	List  []Notification `json:"list"`   // 当前页通知（新在前，按 id 倒序）
}
