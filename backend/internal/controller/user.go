// Package controller HTTP 层：参数绑定校验 → 调 logic → 将结果/错误翻译为统一 JSON 响应。
// 分层约束：不写业务逻辑（全部下沉 logic），不直接调 dao。
package controller

import (
	"campuscommunity/internal/logic"
	"campuscommunity/internal/model"
	"campuscommunity/pkg/consts"
	"campuscommunity/pkg/utils/code"
	"campuscommunity/pkg/utils/response"
	"errors"

	"github.com/gin-gonic/gin"
)

// GetCurrentUserID 从 gin.Context 取当前登录用户 user_id（JWT 中间件注入）。
// key 统一用 pkg/consts.ContextUserIdKey（middleware/jwt/auth.go 写入），
// 导出供后续模块（订单、拼单）controller 复用；只应写在挂了 JWTAuthMiddleware 的路由 handler 里。
func GetCurrentUserID(c *gin.Context) int64 {
	// 中间件保证写入前已完成鉴权，此处类型必然是 int64；断言失败返回零值属编码错误，会在后续查库时暴露
	v, ok := c.Get(consts.ContextUserIdKey)
	userID, _ := v.(int64)
	if !ok || userID <= 0 {
		return 0
	}
	return userID
}

// RegisterHandler 注册接口 POST /api/v1/auth/register（公开）。
func RegisterHandler(c *gin.Context) {
	// 1. 绑定 + 校验请求参数（binding tag 规则不通过走 CodeInvalidParam）
	var p model.ParamRegister
	if err := c.ShouldBindJSON(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. 调 logic 执行注册；业务错误按哨兵映射响应码，其余（DB 挂了等）统一 ServerBusy 不泄露内部细节
	if err := logic.SignUp(&p); err != nil {
		switch {
		case errors.Is(err, logic.ErrUserExist):
			response.ResponseError(c, code.CodeUserExist)
		case errors.Is(err, logic.ErrWeakPassword):
			response.ResponseError(c, code.CodeWeakPassword)
		default:
			response.ResponseError(c, code.CodeServerBusy)
		}
		return
	}
	// 3. 注册成功无数据返回，data 为 nil
	response.ResponseSuccess(c, nil)
}

// LoginHandler 登录接口 POST /api/v1/auth/login（公开），返回 JWT。
func LoginHandler(c *gin.Context) {
	var p model.ParamLogin
	if err := c.ShouldBindJSON(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	token, err := logic.Login(&p)
	if err != nil {
		// 用户不存在与密码错误统一 10005，防撞库（logic 层已合并，这里只需映射）
		if errors.Is(err, logic.ErrWrongLogin) {
			response.ResponseError(c, code.CodeWrongLogin)
			return
		}
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	response.ResponseSuccess(c, gin.H{"token": token})
}

// GetProfileHandler 查看个人资料 GET /api/v1/user/profile（需登录）。
func GetProfileHandler(c *gin.Context) {
	// 当前登录用户 user_id 由 JWT 中间件注入，无需前端传参（防越权：只能查自己）
	userID := GetCurrentUserID(c)
	user, err := logic.GetUserProfile(userID)
	if err != nil {
		if errors.Is(err, logic.ErrUserNotFound) {
			response.ResponseError(c, code.CodeNeedLogin)
			return
		}
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	// 直接序列化 *model.User：Password 字段 json:"-"，哈希不会出现在响应中（model 层安全底线）
	response.ResponseSuccess(c, user)
}

// UpdateProfileHandler 修改个人资料 PATCH /api/v1/user/profile（需登录）。
// PATCH 部分更新语义：前端只提交修改过的字段；未提交字段（JSON 缺失 → 指针为 nil）保持原值。
func UpdateProfileHandler(c *gin.Context) {
	var p model.ParamUpdateProfile
	if err := c.ShouldBindJSON(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	userID := GetCurrentUserID(c)
	if err := logic.UpdateUserProfile(userID, &p); err != nil {
		if errors.Is(err, logic.ErrUserNotFound) {
			response.ResponseError(c, code.CodeNeedLogin)
			return
		}
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	response.ResponseSuccess(c, nil)
}

// UploadAvatarHandler 头像上传 POST /api/v1/user/avatar（需登录，multipart/form-data）。
// 表单字段名 avatar；返回可访问的相对 URL（/uploads/avatars/{user_id}.{ext}）。
func UploadAvatarHandler(c *gin.Context) {
	// 1. 解析 multipart 中的文件字段：缺失/表单非法统一参数错误
	fh, err := c.FormFile("avatar")
	if err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	// 2. userID 来自 JWT（只能改自己的头像，防越权：即使带别人 id 的
	//    请求体也无效——路径与 DB 更新均以 token 身份为准）
	userID := GetCurrentUserID(c)
	url, err := logic.UploadAvatar(userID, fh)
	if err != nil {
		switch {
		case errors.Is(err, logic.ErrAvatarFormat):
			response.ResponseError(c, code.CodeAvatarFormat)
		case errors.Is(err, logic.ErrAvatarTooLarge):
			response.ResponseError(c, code.CodeAvatarLarge)
		case errors.Is(err, logic.ErrUserNotFound):
			response.ResponseError(c, code.CodeNeedLogin)
		default:
			response.ResponseError(c, code.CodeServerBusy)
		}
		return
	}
	// 3. 返回相对 URL：前端拼 base 地址（跨环境迁移不用改 DB 数据）
	response.ResponseSuccess(c, gin.H{"avatar_url": url})
}

// UpdateAddressHandler 修改收货地址 PUT /api/v1/user/address（需登录）。
func UpdateAddressHandler(c *gin.Context) {
	var p model.ParamUpdateAddress
	if err := c.ShouldBindJSON(&p); err != nil {
		response.ResponseError(c, code.CodeInvalidParam)
		return
	}
	userID := GetCurrentUserID(c)
	if err := logic.UpdateUserAddress(userID, &p); err != nil {
		if errors.Is(err, logic.ErrUserNotFound) {
			response.ResponseError(c, code.CodeNeedLogin)
			return
		}
		response.ResponseError(c, code.CodeServerBusy)
		return
	}
	response.ResponseSuccess(c, nil)
}
