package model

// ── 单据地图（用户模块·表）───────────────────────────────
// User → users 表的【档案袋】：一个用户的全量资料（DB 行）
// 用户模块的接口单据（注册/登录/改资料/改地址四张单）在 param_user.go
// ─────────────────────────────────────────────────────────

// User 【档案袋】users 表行：账号、资料、地址全在这一张档案上（DB 映射，model 层）。
// Password 打 json:"-"：档案里的密码页永不外借（见字段注释）。
// 用户业务主键 user_id 由雪花算法生成，对外暴露；内部自增 id 由 BaseModel 提供用于关联。
type User struct {
	BaseModel
	UserID   ID    `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"user_id"`
	Username string `gorm:"type:varchar(50);uniqueIndex;not null;comment:登录账号" json:"username"`
	// Password 存 bcrypt 哈希（cost=10）。json:"-" 确保 API 响应永不泄露哈希，
	// 这是安全底线：即使内部代码误把整个 User 结构体序列化返回，密码也不会出去。
	Password string `gorm:"type:varchar(100);not null;comment:bcrypt哈希" json:"-"`
	Nickname string `gorm:"type:varchar(50);comment:昵称" json:"nickname"`
	Phone    string `gorm:"type:varchar(20);comment:手机号" json:"phone"`
	Avatar   string `gorm:"type:varchar(255);comment:头像URL" json:"avatar"`
	Address  string `gorm:"type:varchar(255);comment:收货地址(单字段)" json:"address"`
}
