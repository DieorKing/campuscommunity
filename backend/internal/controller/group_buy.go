// Package controller HTTP 层：参数绑定校验 → 调 logic → 将结果/错误翻译为统一 JSON 响应。
// 本文件为拼单模块的发布/列表/详情接口。
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

// ListGroupBuyHandler 拼单列表 GET /api/v1/group-buy/list?sort=latest|hot&page=1&page_size=10（可选鉴权）。
// 挂 JWTOptional 中间件：未登录可浏览（is_joined 全 false），登录后附加本人参与标记。
// 参数绑定用 form tag（query 参数），与发布接口的 JSON 绑定（ShouldBindJSON）不同——
// GET 请求无请求体，参数走 URL query，Gin 用 ShouldBindQuery 读取。
func ListGroupBuyHandler(c *gin.Context) {
	// 1. 绑定 query 参数：sort/page/page_size 全部可选，零值由 logic 层归一化，
	//    所以这里不因绑定失败报错（除非类型错误，如 page=abc，才 1001）。
	var p model.ParamListGroupBuy
	if err := c.ShouldBindQuery(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. 取当前用户：JWTOptional 下可能未登录（userID=0），传 0 给 logic 即 is_joined 全 false
	userID := GetCurrentUserID(c)
	// 3. 调 logic 列表；list 接口一般无业务哨兵错误（参数已归一化），
	//    兜底错误统一 ServerBusy——列表失败不该让前端看到内部细节。
	result, err := logic.ListGroupBuy(userID, &p)
	if err != nil {
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	// 4. 成功：返回 {list, total, page, page_size}，前端据此渲染列表与分页
	response.ResponseSuccess(c, result)
}

// GroupBuyDetailHandler 拼单详情 GET /api/v1/group-buy/:id（需登录，强制鉴权组）。
// 与列表接口的两点差异（同是读接口，鉴权策略不同——产品决策而非技术约束）：
//  1. 强制 JWT：详情页是转化入口（看完详情就要抢单），匿名看列表、点详情要求登录，
//     且 is_joined/is_publisher 本人视角字段必须有确定的 userID 才有意义；
//  2. 参数在路径 :id 而非 query——REST 语义：路径标识资源（哪个拼单），query 修饰呈现（怎么排）。
func GroupBuyDetailHandler(c *gin.Context) {
	// 1. 解析路径参数 :id——用户可控输入（不可信边界）：
	//    非数字/越界一律 1001 拦下，绝不带病下传（否则 SQL 层报错变 500，且扩大注入面）。
	idStr := c.Param("id")
	goodID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. 取当前登录用户：强制鉴权中间件已在路由层拦截未登录请求，这里必然拿到有效值。
	userID := GetCurrentUserID(c)
	// 3. 调 logic：哨兵错误映射业务码——「拼单不存在」是用户点了失效链接的正常业务分支（20001），
	//    其余（DB 挂了等）统一 ServerBusy 不泄露内部细节。与发布接口同款 switch 风格保持一致。
	detail, err := logic.GroupBuyDetail(userID, goodID)
	if err != nil {
		switch {
		case errors.Is(err, logic.ErrGroupBuyNotExist):
			response.ResponseError(c, code.CodeGroupBuyNotExist)
		default:
			response.ResponseError(c, code.CodeServerBusy)
		}
		return
	}
	// 4. 成功：返回详情 DTO（商品全量 + 进度 + 参与人员 + is_joined/is_publisher 本人视角），
	//    前端进页一次渲染完成，之后轮询走轻量 /status。
	response.ResponseSuccess(c, detail)
}
