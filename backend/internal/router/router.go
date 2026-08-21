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

	// 拼单模块：发布 + 详情 + 抢单 + 状态轮询（挂 JWT 强制鉴权，四个接口都必须登录）。
	// 详情/抢单/轮询放强制组：详情页是转化入口（看完就要抢单），抢单与轮询依赖
	// 确定的 userID（本人视角/幂等判定），匿名请求无意义。
	groupBuyGroup := v1.Group("/group-buy")
	groupBuyGroup.Use(jwtmid.JWTAuthMiddleware())
	{
		groupBuyGroup.POST("", controller.CreateGroupBuyHandler)    // 发布拼单
		groupBuyGroup.GET("/:id", controller.GroupBuyDetailHandler) // 拼单详情
		groupBuyGroup.POST("/:id/grab", controller.GrabHandler)     // 抢单（受理中）
		groupBuyGroup.GET("/:id/status", controller.StatusHandler)  // 抢单状态轮询（前端 5s 驱动）
	}

	// 拼单模块：列表（可选鉴权——公开浏览 + 登录后附加参与标记，挂 JWTOptional 而非强制鉴权）。
	// 不能放进上面强制鉴权的 groupBuyGroup：列表必须允许匿名访问，只有发布/详情需要登录。
	// 路由树说明：/list（静态段）与 /:id（参数段）在同一位置共存，Gin 匹配静态优先——
	// GET /group-buy/list 命中静态路由，GET /group-buy/123 才落入 :id 参数节点。
	groupBuyPublicGroup := v1.Group("/group-buy")
	groupBuyPublicGroup.Use(jwtmid.JWTOptionalMiddleware())
	{
		groupBuyPublicGroup.GET("/list", controller.ListGroupBuyHandler) // 拼单列表（latest/hot）
	}
	if mode == gin.DebugMode {
		pprof.Register(r)
	}
	r.NoRoute(func(c *gin.Context) {
		response.ResponseErrorWithMsg(c, code.CodeNotFound, "404 NOT FOUND")
	})
	return r
}
