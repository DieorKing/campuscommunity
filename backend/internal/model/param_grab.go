package model

// ── 单据地图（抢单模块·接口）─────────────────────────────
// GrabResult   → 抢单按钮的【回执单】（响应：抢没抢到）
// StatusResult → 轮询弹窗的【进度单】（响应：订单出了没）
// （抢单无请求体——good_id 在 URL 路径、user_id 在 JWT 里，无请求 DTO）
// ─────────────────────────────────────────────────────────

// GrabResult 【回执单】抢单接口响应（POST /api/v1/group-buy/:id/grab）。
// 异步建单的核心体现：抢单成功 ≠ 订单已生成。
// grabbed=true + order_id=null 是合法状态——「受理中」，前端转轮询。
type GrabResult struct {
	Grabbed bool  `json:"grabbed"`  // 是否抢到（Redis 预扣成功）
	OrderID int64 `json:"order_id"` // 订单号；0 = 受理中（MQ 消费者尚未建单），前端继续轮询 /status
}

// StatusResult 【进度单】抢单状态轮询响应（GET /api/v1/group-buy/:id/status，前端 5s 轮询）。
// 三态语义（按优先级判断）：
//   未参与（不在 members 集合）→ grabbed=false，无需再看订单
//   已参与 + 订单未生成         → grabbed=true, order_id=0（受理中，继续轮询）
//   已参与 + 订单已生成         → grabbed=true, order_id=非0（轮询终点，跳支付页）
type StatusResult struct {
	Grabbed bool  `json:"grabbed"`
	OrderID int64 `json:"order_id"` // 0 = 受理中
}
