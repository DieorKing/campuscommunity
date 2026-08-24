package jwt

import (
	"campuscommunity/pkg/consts"
	"campuscommunity/pkg/utils/jwt"
	"strings"

	"github.com/gin-gonic/gin"
)

// JWTOptionalMiddleware 可选鉴权中间件：登录态可选，未登录也能访问。
// 与 JWTAuthMiddleware（强制鉴权）的核心差异：
//   - 强制版：无 token / token 无效 → 直接 401 中断请求
//   - 可选版：无 token → 放行；token 无效 → 放行但视为未登录；token 有效 → 注入 user_id
//
// 适用场景：公开接口但登录后提供差异化信息。本项目的拼单列表——
// 未登录可浏览全部拼单；登录后每项附加 is_joined（本人参与标记），
// 而 is_joined 依赖当前登录用户的 user_id，只能从 token 取。
// 为什么 token 无效也放行而不是拒绝：
//   列表本身是公开资源，不因「一个坏 token」阻塞浏览（这是公开接口的容错语义）；
//   若严格拒绝，前端 token 过期时会看到列表接口 401，与「未登录可浏览」的设计矛盾。
//   注意取舍：坏 token 会被静默当匿名处理（is_joined 全 false），前端 token 失效后
//   应自行跳转登录，由前端的 401 拦截器（或登录态校验）负责，而非本中间件。
func JWTOptionalMiddleware() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 1. 读 Authorization 头，无 token 直接放行（匿名浏览）
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}
		// 2. 校验 Bearer 前缀格式；格式不对视为未登录放行
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.Next()
			return
		}
		// 3. 解析 token；解析失败（过期/篡改）视为未登录放行
		token, err := jwt.ParseToken(parts[1])
		if err != nil {
			c.Next()
			return
		}
		// 4. 解析成功：注入 user_id，后续 handler 可通过 GetCurrentUserID(c) 获取
		c.Set(consts.ContextUserIdKey, token.UserId)
		c.Next()
	}
}
