package router

import (
	"campuscommunity/internal/controller"
	jwtmid "campuscommunity/internal/middleware/jwt"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/logger"
	"campuscommunity/pkg/utils/response"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
)

func SetupRouter(mode string) *gin.Engine {
	if mode == gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	//全局中间件日志
	r.Use(logger.GinLogger(), logger.GinRecovery(true))

	v1 := r.Group("/api/v1")

	v1.GET("/test", func(c *gin.Context) {
		response.ResponseSuccess(c, "OK")
	})

	// 用户模块：注册/登录（公开接口，无需鉴权）
	authGroup := v1.Group("/auth")
	{
		authGroup.POST("/register", controller.RegisterHandler) // 注册
		authGroup.POST("/login", controller.LoginHandler)       // 登录，返回 JWT
	}

	// 用户模块：个人资料/收货地址（挂 JWT 鉴权中间件，组内路由全部需登录）
	userGroup := v1.Group("/user")
	userGroup.Use(jwtmid.JWTAuthMiddleware())
	{
		userGroup.GET("/profile", controller.GetProfileHandler)      // 查看个人资料
		userGroup.PATCH("/profile", controller.UpdateProfileHandler) // 修改个人资料（PATCH 部分字段更新）
		userGroup.PUT("/address", controller.UpdateAddressHandler)   // 修改收货地址
	}
	if mode == gin.DebugMode {
		pprof.Register(r)
	}
	r.NoRoute(func(c *gin.Context) {
		response.ResponseErrorWithMsg(c, code.CodeNotFound, "404 NOT FOUND")
	})
	return r
}
