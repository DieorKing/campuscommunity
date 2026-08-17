package mysql

import "campuscommunity/internal/model"

// Migration 自动建表：对比结构体与线上表，缺表建表、缺列加列。
// 结构体即建表语句（单一事实来源），不单独维护 SQL DDL 文件。
// 注意：AutoMigrate 只增不减——删字段/改类型不会自动改线上表，开发期需手动 DROP 后重启重建。
// 新增业务表时，在此追加 &model.Xxx{} 即可。
func Migration() error {
	return db.AutoMigrate(
		&model.User{},
		&model.GroupBuy{},
		&model.GroupBuyMember{},
		&model.Order{},
		&model.Notification{},
	)
}
