package model

// ── 单据地图（用户模块·接口）─────────────────────────────
// ParamRegister      → 注册页的【入会申请单】（请求）
// ParamLogin         → 登录页的【身份核对单】（请求）
// ParamUpdateProfile → 个人资料页的【修改单】（请求，PATCH 部分更新）
// ParamUpdateAddress → 收货地址的【修改单】（请求，PUT 整体替换）
// ─────────────────────────────────────────────────────────

// ParamRegister 【入会申请单】注册页提交（POST /api/v1/auth/register，请求体 JSON）。
// binding 校验只做「格式硬约束」（必填/长度），业务规则（密码强度含字母+数字）在 logic 层做。
type ParamRegister struct {
	Username        string `json:"username" binding:"required,min=3,max=50"`             // 登录账号，3-50 字符
	Password        string `json:"password" binding:"required,min=8,max=64"`             // 明文密码（仅传输层可见，落库前 logic 层哈希）
	ConfirmPassword string `json:"confirm_password" binding:"required,eqfield=Password"` // 确认密码，必须与 Password 一致（eqfield 交叉校验）
}

// ParamLogin 【身份核对单】登录页提交（POST /api/v1/auth/login，请求体 JSON）。
type ParamLogin struct {
	Username string `json:"username" binding:"required"` // 登录账号
	Password string `json:"password" binding:"required"` // 明文密码
}

// ParamUpdateProfile 【修改单·资料】个人资料页提交（PATCH /api/v1/user/profile，请求体 JSON）。
// PATCH 部分更新语义：字段用 *string 指针区分「未提交(nil)」与「提交了空串(清空)」——
// nil=该字段不更新；""=显式提交空串，清空字段。前端只提交修改过的字段即可。
// binding 用 omitempty：允许字段缺省（缺省=不更新），同时对提交的值做长度上限校验。
type ParamUpdateProfile struct {
	Nickname *string `json:"nickname" binding:"omitempty,max=50"` // 昵称，nil=不更新，""=清空
	Phone    *string `json:"phone" binding:"omitempty,max=20"`    // 手机号（MVP 不做格式校验，仅长度）
	Avatar   *string `json:"avatar" binding:"omitempty,max=255"`  // 头像 URL，nil=不更新，""=清空
}

// ParamUpdateAddress 【修改单·地址】收货地址提交（PUT /api/v1/user/address，请求体 JSON）。
// 地址为单字段存储（mvp §1.4），后端仅做长度校验。
type ParamUpdateAddress struct {
	Address string `json:"address" binding:"required,max=255"` // 收货地址
}
