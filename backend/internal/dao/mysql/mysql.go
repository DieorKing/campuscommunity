package mysql

import (
	"campuscommunity/internal/conf"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// db 是 GORM 的全局 DB 实例，包私有，外部通过 GetDB() 获取。
// 与 redis.go 的 client 私有风格保持一致，避免外部包随意替换。
var db *gorm.DB

// Init 初始化 MySQL 连接。
// cfg 由 conf.Conf.MySQLConfig 注入，字段来自 config.yaml 的 mysql 段。
// 流程：拼 DSN → gorm.Open → 取底层 *sql.DB 设连接池 → ping 验证可用。
// 失败返回 err，调用方（main.go）应 log.Fatal 退出，不可降级运行。
// 注：本文件只负责连接初始化；AutoMigrate 在 migrate.go 中执行。
func Init(cfg *conf.MySQLConfig) (err error) {
	// 拼接 DSN：username:password@tcp(host:port)/database?charset=utf8mb4&parseTime=true&loc=Local
	// parseTime=true 让 MySQL 的 DATETIME 自动映射到 time.Time
	// loc=Local 让时间按本地时区解析
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%t&loc=%s",
		cfg.Username, cfg.Password,
		cfg.Host, cfg.Port,
		cfg.Database, cfg.Charset,
		cfg.ParseTime, cfg.Loc,
	)

	// gorm.Open 建立连接并立即 ping；若 DB 不存在或密码错会在此返回 err
	// Logger 用 GORM 默认 logger + Warn 级别（只打慢查询与错误），MVP 不接入 zap adapter
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("gorm open: %w", err)
	}

	// 取底层 *sql.DB 以设置连接池参数与 ping
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying *sql.DB: %w", err)
	}
	// 连接池三参数（压测调优：负缩放的主因是空闲连接不足引发的连接风暴——
	// 高频短查询下重新建连（TCP+MySQL 认证握手）的开销远大于查询本身）：
	//   MaxOpen：并发上限，超出排队
	//   MaxIdle ≈ MaxOpen：归还的连接全部保活，杜绝「用完即关、下波重建」的
	//     连接风暴——用少量常驻内存（每连接 ~1MB）换建连开销与 TIME_WAIT 堆积
	//   ConnMaxLifetime：连接最长寿命，到期主动换新——防长命连接被 MySQL
	//     wait_timeout 悄悄杀掉后出现「使用已关闭连接」的诡异错误
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	if cfg.ConnMaxLifetime != "" {
		lifetime, err := time.ParseDuration(cfg.ConnMaxLifetime)
		if err != nil {
			return fmt.Errorf("parse conn_max_lifetime: %w", err)
		}
		sqlDB.SetConnMaxLifetime(lifetime)
	}

	// ping验证连接真正可用
	if err = sqlDB.Ping(); err != nil {
		return fmt.Errorf("mysql ping: %w", err)
	}
	return nil
}

// GetDB 返回全局GORM DB实例，各模块使用。
// 调用前 Init 必须已完成，否则返回 nil。
func GetDB() *gorm.DB {
	return db
}

// Close 闭数据库连接，main.go 退出时defer调用。
// GORM没有直接Close,需取底层*sql.DB再Close。
func Close() {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	_ = sqlDB.Close()
}
