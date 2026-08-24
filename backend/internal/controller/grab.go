// Package controller HTTP 层：参数绑定校验 → 调 logic → 将结果/错误翻译为统一 JSON 响应。
// 本文件为抢单模块的抢单与状态轮询接口。
package controller

import (
	"campuscommunity/internal/logic"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/response"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
)

// GrabHandler 抢单 POST /api/v1/group-buy/:id/grab（需登录，强制鉴权组）。
// 异步建单约定：成功响应是「受理中」（grabbed=true, order_id=0），
// 订单号由前端轮询 /status 获取——抢单请求本身不等待建单完成。
func GrabHandler(c *gin.Context) {
	// 1. 解析路径参数 :id（用户可控输入，不可信边界）：非数字一律 1001 拦下
	idStr := c.Param("id")
	goodID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. 当前登录用户由强制鉴权中间件注入
	userID := GetCurrentUserID(c)
	// 3. 调 logic 六步编排；业务哨兵逐个映射业务码：
	//    售罄(20004)/重复参与(20005)/发布者(20006)/繁忙(20007)语义各不相同——
	//    前端据此决定「死心」还是「重试」，映射错一个用户体验就错一个量级
	result, err := logic.GrabGroupBuy(userID, goodID)
	if err != nil {
		switch {
		case errors.Is(err, logic.ErrGroupBuyNotExist):
			response.ResponseError(c, code.CodeGroupBuyNotExist)
		case errors.Is(err, logic.ErrGroupBuyExpired):
			response.ResponseError(c, code.CodeGroupBuyExpired)
		case errors.Is(err, logic.ErrGrabSoldOut):
			response.ResponseError(c, code.CodeGrabSoldOut)
		case errors.Is(err, logic.ErrGrabDuplicate):
			response.ResponseError(c, code.CodeGrabDuplicate)
		case errors.Is(err, logic.ErrGrabPublisher):
			response.ResponseError(c, code.CodeGrabPublisher)
		case errors.Is(err, logic.ErrGrabBusy):
			response.ResponseError(c, code.CodeGrabBusy)
		default:
			response.ResponseError(c, code.CodeServerBusy)
		}
		return
	}
	// 4. 受理中：grabbed=true, order_id=0，前端转入 5s 轮询
	response.ResponseSuccess(c, result)
}

// StatusHandler 抢单状态轮询 GET /api/v1/group-buy/:id/status（需登录，强制鉴权组）。
// 无状态接口：每次请求独立完成「参与判定 → 订单判定」，循环由前端定时器驱动，
// 后端不记录轮询进度——服务可水平扩展，任意实例可应答任意一次轮询。
// 响应三态：{false,0} 未抢到 / {true,0} 受理中 / {true,非0} 建单完成。
func StatusHandler(c *gin.Context) {
	// 1. 解析路径参数 :id，同抢单接口的边界拦截
	idStr := c.Param("id")
	goodID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. 当前登录用户
	userID := GetCurrentUserID(c)
	// 3. 查状态：本接口无业务哨兵（拼单不存在与未参与同返回 grabbed=false——
	//    轮询仅在前端抢单受理后发起，无需区分这两种「没抢到」）
	result, err := logic.GetGrabStatus(userID, goodID)
	if err != nil {
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	// 4. 返回三态，前端按状态机决定停轮询/继续转圈/跳转支付
	response.ResponseSuccess(c, result)
}
