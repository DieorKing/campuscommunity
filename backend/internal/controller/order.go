// Package controller HTTP 层：参数绑定校验 → 调 logic → 将结果/错误翻译为统一 JSON 响应。
// 本文件为订单模块的 4 个接口：模拟支付 / 取消 / 列表 / 详情。
package controller

import (
	"campuscommunity/internal/logic"
	"campuscommunity/internal/model"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/response"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
)

// parseOrderID 解析路径参数 :id 为 order_id（订单接口共用）。
// 用户可控输入，不可信边界：非数字一律 1001 拦下。
func parseOrderID(c *gin.Context) (int64, bool) {
	idStr := c.Param("id")
	orderID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return 0, false
	}
	return orderID, true
}

// mapOrderErr 订单哨兵 → 业务码的统一映射（Pay/Cancel/Detail 三接口共用）。
// 不存在 30001 / 越权 1003（通用 Forbidden，不暴露他人订单存在性）/
// 状态机拒绝 30002（前端提示「请刷新」而非报错——双击是常态不是事故）。
func mapOrderErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, logic.ErrOrderNotExist):
		response.ResponseError(c, code.CodeOrderNotExist)
	case errors.Is(err, logic.ErrOrderNotOwner):
		response.ResponseError(c, code.CodeForbidden)
	case errors.Is(err, logic.ErrOrderStatusChanged):
		response.ResponseError(c, code.CodeOrderStatusChanged)
	default:
		response.ResponseError(c, code.CodeServerBusy)
	}
}

// PayHandler 模拟支付 POST /api/v1/order/:id/pay（需登录）。
// mock 语义：无真实支付网关，但状态迁移走条件 UPDATE 状态机守卫
// ——双击支付的第二击被 WHERE status='pending_pay' 物理拒绝（状态机幂等防线）。
func PayHandler(c *gin.Context) {
	orderID, ok := parseOrderID(c)
	if !ok {
		return
	}
	userID := GetCurrentUserID(c)
	if err := logic.PayOrder(userID, orderID); err != nil {
		mapOrderErr(c, err)
		return
	}
	response.ResponseSuccess(c, nil)
}

// CancelHandler 取消订单 POST /api/v1/order/:id/cancel（需登录）。
// 仅 pending_pay 可取消：已支付/已关闭/已取消一律 30002。
// 取消成功后名额释放（人数-1 同事务 + Redis INCR/SREM best-effort）。
func CancelHandler(c *gin.Context) {
	orderID, ok := parseOrderID(c)
	if !ok {
		return
	}
	userID := GetCurrentUserID(c)
	if err := logic.CancelOrder(userID, orderID); err != nil {
		mapOrderErr(c, err)
		return
	}
	response.ResponseSuccess(c, nil)
}

// ListOrdersHandler 我的订单列表 GET /api/v1/order/list（需登录）。
// query 参数：status（可选过滤）/ page / page_size（logic 层归一化）。
// 注意与 /order/:id 的路由共存：/list 是静态段，Gin 静态优先匹配，
// GET /order/123 才落入 :id 参数节点（与 group-buy 路由树结构一致）。
func ListOrdersHandler(c *gin.Context) {
	var p model.ParamListOrder
	// query 绑定失败（如 page=abc）→ 1001；全可选参数下极少触发
	if err := c.ShouldBindQuery(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	userID := GetCurrentUserID(c)
	result, err := logic.ListUserOrders(userID, &p)
	if err != nil {
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	response.ResponseSuccess(c, result)
}

// OrderDetailHandler 订单详情 GET /api/v1/order/:id（需登录）。
// 资源所有权校验在 logic（非本人 = 越权，不暴露他人订单数据）。
func OrderDetailHandler(c *gin.Context) {
	orderID, ok := parseOrderID(c)
	if !ok {
		return
	}
	userID := GetCurrentUserID(c)
	order, err := logic.GetOrderDetail(userID, orderID)
	if err != nil {
		mapOrderErr(c, err)
		return
	}
	response.ResponseSuccess(c, order)
}
