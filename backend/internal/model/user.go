package model

// User 用户表：注册登录、个人资料、收货地址（单字段，不走多表）。
// 用户业务主键 user_id 由雪花算法生成，对外暴露；内部自增 id 由 BaseModel 提供用于关联。
type User struct {
	BaseModel
	UserID   int64  `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"user_id"`
	Username string `gorm:"type:varchar(50);uniqueIndex;not null;comment:登录账号" json:"username"`
	// Password 存 bcrypt 哈希（cost=10）。json:"-" 确保 API 响应永不泄露哈希，
	// 这是安全底线：即使内部代码误把整个 User 结构体序列化返回，密码也不会出去。
	Password string `gorm:"type:varchar(100);not null;comment:bcrypt哈希" json:"-"`
	Nickname string `gorm:"type:varchar(50);comment:昵称" json:"nickname"`
	Phone    string `gorm:"type:varchar(20);comment:手机号" json:"phone"`
	Avatar   string `gorm:"type:varchar(255);comment:头像URL" json:"avatar"`
	Address  string `gorm:"type:varchar(255);comment:收货地址(单字段)" json:"address"`
}
