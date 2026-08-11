package router

import (
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
	if mode == gin.DebugMode {
		pprof.Register(r)
	}
	r.NoRoute(func(c *gin.Context) {
		response.ResponseErrorWithMsg(c, code.CodeNotFound, "404 NOT FOUND")
	})
	return r
}
