// 订单模块 API：支付 / 取消 / 列表 / 详情
import request from '../utils/request'

// 模拟支付（POST /order/:id/pay）：空请求体，订单号在路径，支付人在 token
// 双击第二击由后端状态机拒绝（30002），前端无需防抖兜底
export function payOrder(orderId) {
  return request.post(`/order/${orderId}/pay`)
}

// 取消订单（POST /order/:id/cancel）：仅 pending_pay 可取消
export function cancelOrder(orderId) {
  return request.post(`/order/${orderId}/cancel`)
}

// 我的订单列表（GET /order/list?status=&page=&page_size=）
// status 空 = 全部：pending_pay/paid/cancelled/completed/closed
export function listOrders(params) {
  return request.get('/order/list', { params })
}

// 订单详情（GET /order/:id）：仅本人可见（非本人 1003）
export function getOrderDetail(orderId) {
  return request.get(`/order/${orderId}`)
}
