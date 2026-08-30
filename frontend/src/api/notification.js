// 通知模块 API：列表（30s 轮询数据源）/ 标记已读
import request from '../utils/request'

// 我的通知列表（GET /notification/list?page=&page_size=）
// 响应：{ total, unread, list }——列表+未读角标一次返回（读合并）
// 注意：notification_id 是字符串（后端 ID MarshalJSON 防精度丢失），
// 拼 URL 直接用字符串，前端永远不要 parseInt 雪花 ID
export function listNotifications(params) {
  return request.get('/notification/list', { params })
}

// 标记单条通知已读（POST /notification/:id/read）
// 幂等：已读再标返回 code=0（后端 rows=0 静默成功），前端重复点击无感
export function markNotificationRead(notificationId) {
  return request.post(`/notification/${notificationId}/read`)
}
