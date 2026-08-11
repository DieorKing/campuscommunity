package jwt

import (
	"campuscommunity/pkg/consts"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/jwt"
	"campuscommunity/pkg/utils/response"
	"strings"

	"github.com/gin-gonic/gin"
)

func JWTAuthMiddleware() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 客户端携带Token有三种方式 1.放在请求头 2.放在请求体 3.放在URI
		// 这里让Token放在Header的Authorization中，并使用Bearer开头
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			response.ResponseError(c, code.CodeNeedLogin)
			c.Abort()
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			response.ResponseError(c, code.CodeInvalidToken)
			c.Abort()
			return
		}
		token, err := jwt.ParseToken(parts[1])
		if err != nil {
			response.ResponseError(c, code.CodeInvalidToken)
			c.Abort()
			return
		}
		c.Set(consts.ContextUserIdKey, token.UserId)

		c.Next() // 后续的处理请求的函数中 可以用过c.Get(CtxUserIDKey) 来获取当前请求的用户信息
	}
}
