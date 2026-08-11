package model

import "time"

// BaseModel 所有业务表的公共字段集合，通过结构体嵌入复用。
// 只含内部主键和时间戳，业务键{模块}_id 命名。
// demo 不用全部软删除（订单/通知用 status 流转，无用户注销接口），不含 DeletedAt。
// 可扩展软删除，各表自行添加 DeletedAt gorm.DeletedAt。
type BaseModel struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;comment:内部主键" json:"-"` // 内部主键，外键关联用
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
