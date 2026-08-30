// 拼单模块 API：发布 / 列表 / 详情 / 抢单 / 状态轮询
import request from '../utils/request'

// 发布拼单（POST /group-buy）
// 参数与后端 ParamCreateGroupBuy 对齐：deadline 为 RFC3339 时间串
export function createGroupBuy(data) {
  return request.post('/group-buy', data)
}

// 拼单列表（GET /group-buy/list?sort=latest|hot&page=&page_size=）
// sort 缺省 latest；列表无需登录，登录后每项附加 is_joined
export function listGroupBuy(params) {
  return request.get('/group-buy/list', { params })
}

// 拼单详情（GET /group-buy/:id）
export function getGroupBuyDetail(goodId) {
  return request.get(`/group-buy/${goodId}`)
}

// 抢单（POST /group-buy/:id/grab）
// 异步建单：成功响应是「受理中」(grabbed=true, order_id=0)，订单号靠轮询拿
export function grabGroupBuy(goodId) {
  return request.post(`/group-buy/${goodId}/grab`)
}

// 抢单状态轮询（GET /group-buy/:id/status）
// 三态：{grabbed:false} 未抢到(停) / {grabbed:true,order_id:0} 受理中(继续)
//       {grabbed:true,order_id:非0} 建单完成(停+跳转)
export function getGrabStatus(goodId) {
  return request.get(`/group-buy/${goodId}/status`)
}
