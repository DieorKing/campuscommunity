// Package controller HTTP 层：参数绑定校验 → 调 logic → 将结果/错误翻译为统一 JSON 响应。
// 本文件为拼单模块的发布接口；列表/详情等 Handler 在后续功能中追加。
package controller

import (
	"campuscommunity/internal/logic"
	"campuscommunity/internal/model"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/response"
	"errors"

	"github.com/gin-gonic/gin"
)

// CreateGroupBuyHandler 发布拼单 POST /api/v1/group-buy（需登录）。
// 流程：绑定请求 → 取当前用户 → 调 logic 发布 → 成功返回 good_id，失败按哨兵错误映射响应码。
func CreateGroupBuyHandler(c *gin.Context) {
	// 1. 绑定 + 校验请求参数：binding 格式规则不通过统一 1001（与用户模块一致）
	var p model.ParamCreateGroupBuy
	if err := c.ShouldBindJSON(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. 当前登录用户 user_id 由 JWT 中间件注入（不用前端传，防伪造发布者）
	userID := GetCurrentUserID(c)
	// 3. 调 logic 发布；业务错误按哨兵映射，其余（DB/Redis 挂了等）统一 ServerBusy 不泄露内部细节
	goodID, err := logic.CreateGroupBuy(userID, &p)
	if err != nil {
		switch {
		case errors.Is(err, logic.ErrGroupBuyInvalid):
			response.ResponseError(c, code.CodeGroupBuyInvalid)
		case errors.Is(err, logic.ErrGroupBuyExpired):
			response.ResponseError(c, code.CodeGroupBuyExpired)
		default:
			response.ResponseError(c, code.CodeServerBusy)
		}
		return
	}
	// 4. 发布成功：返回 good_id，前端据此跳转拼单详情页
	response.ResponseSuccess(c, gin.H{"good_id": goodID})
}
