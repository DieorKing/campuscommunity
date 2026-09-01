// Package ratelimit 限流中间件：令牌桶算法，按用户维度精细限流。
// 本文件为 Gin 中间件层：从上下文取 userID 作限流 key，调 Limiter
// 判定，超限返回 50xxx 业务码。必须挂在 JWT 鉴权之后（key 依赖
// JWT 中间件注入的 user_id；顺序反了 key 恒为空）。
package ratelimit

import (
	"campuscommunity/pkg/consts"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

// UserRateLimitMiddleware 用户维度限流中间件（挂 JWT 之后）。
// limiter：限流器实例（调用方构造并注入，中间件无状态可全局复用）。
func UserRateLimitMiddleware(limiter Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 取限流 key：JWT 中间件已注入的 user_id。鉴权失败的请求
		// 在前置中间件已被拦截，此处 userID 必然有效。
		var key string
		if v, ok := c.Get(consts.ContextUserIdKey); ok {
			if userID, ok := v.(int64); ok && userID > 0 {
				key = formatKey(userID)
			}
		}
		if key == "" {
			// key 为空（理论不可达的防御分支）：放行——限流组件自身
			// 异常不应阻断业务请求
			c.Next()
			return
		}
		if !limiter.Allow(key) {
			// 限流命中：直接终结请求（不进 handler），拒绝成本仅为
			// 一条 JSON 响应（快速失败）
			response.ResponseError(c, code.CodeRateLimited)
			c.Abort()
			return
		}
		c.Next()
	}
}

// formatKey userID 转限流 key（前缀区隔，便于未来多维度扩展调试）。
func formatKey(userID int64) string {
	return "u:" + strconv.FormatInt(userID, 10)
}

// IPRateLimitMiddleware IP 维度限流中间件（挂公开接口，无需 JWT，如注册防脚本）。
func IPRateLimitMiddleware(limiter Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 容器栈经 Nginx 反代：Nginx 已设置 X-Forwarded-For，ClientIP 可还原
		// 真实客户端 IP；X-Forwarded-For 可伪造属已知边界（MVP 接受，
		// 严谨做 SetTrustedProxies 白名单
		key := formatIPKey(c.ClientIP())
		if key == "" {
			// 防御分支：限流组件自身异常不阻断业务
			c.Next()
			return
		}
		if !limiter.Allow(key) {
			response.ResponseError(c, code.CodeRateLimited)
			c.Abort()
			return
		}
		c.Next()
	}
}

// formatIPKey IP 转限流 key（前缀区隔，与 formatKey 同构：u: / ip:）。
func formatIPKey(ip string) string {
	return "ip:" + ip
}
