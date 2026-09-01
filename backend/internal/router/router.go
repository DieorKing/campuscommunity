package router

import (
	"campuscommunity/internal/conf"
	"campuscommunity/internal/controller"
	jwtmid "campuscommunity/internal/middleware/jwt"
	"campuscommunity/internal/middleware/ratelimit"
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

	// 用户模块：注册/登录（公开接口，无需鉴权）。
	// 注册接口挂 IP 维度限流（公开接口无登录态拿不到 userID，只能按
	// 来源 IP——与抢单接口的 userID 维度形成场景互补：有身份用身份，
	// 无身份用 IP，同一个 Limiter 接口两种 keyFunc 零改动）。
	// login 不挂：登录是存量用户的高频正常行为（换设备/重连都要登录，
	// 输错密码还会重试），IP 维度限流在 NAT 出口下会误伤共享出口的
	// 合法用户——防脚本注册用验证码演进（限流挡流量，验证码挡人机）。
	registerLimiter := ratelimit.NewMemoryLimiter(0.1, 3)
	authGroup := v1.Group("/auth")
	{
		authGroup.POST("/register", ratelimit.IPRateLimitMiddleware(registerLimiter), controller.RegisterHandler) // 注册（IP 限流内）
		authGroup.POST("/login", controller.LoginHandler)                                                         // 登录，返回 JWT
	}

	// 用户模块：个人资料/收货地址（挂 JWT 鉴权中间件，组内路由全部需登录）
	userGroup := v1.Group("/user")
	userGroup.Use(jwtmid.JWTAuthMiddleware())
	{
		userGroup.GET("/profile", controller.GetProfileHandler)      // 查看个人资料
		userGroup.PATCH("/profile", controller.UpdateProfileHandler) // 修改个人资料（PATCH 部分字段更新）
		userGroup.PUT("/address", controller.UpdateAddressHandler)   // 修改收货地址
		userGroup.POST("/avatar", controller.UploadAvatarHandler)    // 头像上传（multipart，JWT 内）
	}

	// 上传文件静态服务：dev 直连后端时由 Gin 托管 {upload.dir}；
	// 容器栈走 Nginx 的 /uploads/ 反代（见 nginx.conf），此路由兜底不影响。
	// 配置未设置 upload.dir（旧配置文件兼容）时跳过注册，不影响启动。
	if dir := conf.Conf.UploadConfig.Dir; dir != "" {
		r.Static("/uploads", dir)
	}

	// 拼单模块：发布 + 详情 + 抢单 + 状态轮询（挂 JWT 强制鉴权，四个接口都必须登录）。
	// 详情/抢单/轮询放强制组：详情页是转化入口（看完就要抢单），抢单与轮询依赖
	// 确定的 userID（本人视角/幂等判定），匿名请求无意义。
	// 抢单/轮询路由额外挂用户维度令牌桶限流（JWT 之后）。
	// 参数依据：单用户 rate 参照合法用户行为上限——前端轮询 5s 一次 +
	// 手点重试，正常峰值约 2~3 req/s，取 5/s 留一倍余量；桶容量 10
	// 覆盖双击与快速重试的瞬时突发。脚本级高频请求在 10 发后被拒，
	// 单用户无法占满系统容量（全系统级总闸属网关层职责，不在单机
	// 中间件实现——分层限流：网关挡总量，应用层挡单用户）。
	grabLimiter := ratelimit.NewMemoryLimiter(5, 10)
	groupBuyGroup := v1.Group("/group-buy")
	groupBuyGroup.Use(jwtmid.JWTAuthMiddleware())
	{
		groupBuyGroup.POST("", controller.CreateGroupBuyHandler)    // 发布拼单
		groupBuyGroup.GET("/:id", controller.GroupBuyDetailHandler) // 拼单详情
		grabGroup := groupBuyGroup.Group("")
		grabGroup.Use(ratelimit.UserRateLimitMiddleware(grabLimiter))
		{
			grabGroup.POST("/:id/grab", controller.GrabHandler)    // 抢单（受理中，限流内）
			grabGroup.GET("/:id/status", controller.StatusHandler) // 抢单状态轮询（前端 5s 驱动，限流内）
		}
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
	// 订单模块：支付/取消/列表/详情（挂 JWT 强制鉴权——订单是私产，
	// 全部接口依赖确定的 userID 做所有权校验，匿名无意义）。
	// 路由树：/list（静态段）与 /:id（参数段）同位共存，Gin 静态优先
	//（与 group-buy 路由树结构一致），GET /order/list 命中列表，GET /order/123 落 :id。
	orderGroup := v1.Group("/order")
	orderGroup.Use(jwtmid.JWTAuthMiddleware())
	{
		orderGroup.POST("/:id/pay", controller.PayHandler)       // 模拟支付（状态机守卫）
		orderGroup.POST("/:id/cancel", controller.CancelHandler) // 仅 pending_pay 可取消
		orderGroup.GET("/list", controller.ListOrdersHandler)    // 我的订单（状态筛选+分页）
		orderGroup.GET("/:id", controller.OrderDetailHandler)    // 订单详情（所有权校验）
	}

	// 通知模块：列表 + 标记已读（挂 JWT 强制鉴权——通知是私产，
	// 列表/已读都依赖确定的 userID 做归属，匿名无意义）。
	// 路由树：/list（静态段）与 /:id/read（参数段）同位共存，Gin 静态优先
	//（与 group-buy/order 路由树结构一致）。
	notificationGroup := v1.Group("/notification")
	notificationGroup.Use(jwtmid.JWTAuthMiddleware())
	{
		notificationGroup.GET("/list", controller.ListNotificationsHandler)         // 我的通知（30s 轮询数据源，列表+未读一次返回）
		notificationGroup.POST("/:id/read", controller.MarkNotificationReadHandler) // 标记已读（幂等：已读再标成功）
	}

	if mode == gin.DebugMode {
		pprof.Register(r)
	}
	r.NoRoute(func(c *gin.Context) {
		response.ResponseErrorWithMsg(c, code.CodeNotFound, "404 NOT FOUND")
	})
	return r
}
