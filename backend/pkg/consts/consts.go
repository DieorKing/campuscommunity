// Package consts 存放跨层共享的常量。
// 放在此包而非 controller/logic/dao 任何一层，
// 是因为常量会被多层引用，不应归属于某一具体业务层。
package consts

// ContextUserIdKey 是 gin.Context 中存取当前登录用户 ID 的 key。
// JWT 中间件解析 token 后 c.Set(ContextUserIdKey, userID)，
// 后续 controller/logic 通过 c.Get(ContextUserIdKey) 取出。
const ContextUserIdKey = "user_id"
