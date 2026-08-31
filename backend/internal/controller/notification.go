// Package controller HTTP 层：参数绑定校验 → 调 logic → 将结果/错误翻译为统一 JSON 响应。
// 本文件为通知模块的 2 个接口：通知列表（30s 轮询数据源）/ 标记已读。
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

// mapNotificationErr 通知哨兵 → 业务码的统一映射（MarkRead 用）。
// 不存在 40001 / 越权 1003（通用 Forbidden，不暴露他人通知存在性——
// 与订单越权同语义：报「无权限」而非「非本人」）。
func mapNotificationErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, logic.ErrNotificationNotExist):
		response.ResponseError(c, code.CodeNotificationNotExist)
	case errors.Is(err, logic.ErrNotificationNotOwner):
		response.ResponseError(c, code.CodeForbidden)
	default:
		response.ResponseError(c, code.CodeServerBusy)
	}
}

// ListNotificationsHandler 我的通知列表 GET /api/v1/notification/list（需登录）。
// 前端 30s 轮询的数据源：列表 + 未读角标一次返回（读合并）。
// 路由树：/list 静态段与 /:id 参数段同位共存，Gin 静态优先
// （与 group-buy/order 路由树结构一致）。
func ListNotificationsHandler(c *gin.Context) {
	var p model.ParamListNotification
	// query 绑定失败（如 page=abc）→ 1001；全可选参数下极少触发
	if err := c.ShouldBindQuery(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	userID := GetCurrentUserID(c)
	result, err := logic.ListNotifications(userID, &p)
	if err != nil {
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	response.ResponseSuccess(c, result)
}

// MarkNotificationReadHandler 标记单条通知已读 POST /api/v1/notification/:id/read（需登录）。
// 已读再标已读 = 幂等成功（不报错）——前端重复点击的第二击收 0。
func MarkNotificationReadHandler(c *gin.Context) {
	// 路径参数解析：用户可控输入，非数字一律 1001 拦下（与 parseOrderID 同一模式）
	idStr := c.Param("id")
	notificationID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	userID := GetCurrentUserID(c)
	if err := logic.MarkNotificationRead(userID, notificationID); err != nil {
		mapNotificationErr(c, err)
		return
	}
	response.ResponseSuccess(c, nil)
}
