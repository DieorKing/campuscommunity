package conf

import "time"

// ── 单据地图（本文件）─────────────────────────────────────
// ServerConfig → 【config.yaml 的 Go 镜像】：根节点嵌 6 个子配置
// 嵌套判据：配置文件有层级 → 嵌套（YAML 的 log:/mysql:/redis: 一层对一层）
// ──────────────────────────────────────────────────────────

// 全局配置单例：Init 时 Viper 反序列化填充，进程内任意包 conf.Conf 直接读。
var Conf = new(ServerConfig)

// ServerConfig 【config.yaml 根节点】服务配置总入口，嵌各子配置（形状跟 YAML 层级走）。
type ServerConfig struct {
	Name             string `mapstructure:"name"`
	Mode             string `mapstructure:"run_mode"`
	Port             int    `mapstructure:"port"`
	*LogConfig       `mapstructure:"log"`
	*MySQLConfig     `mapstructure:"mysql"`
	*RedisConfig     `mapstructure:"redis"`
	*RabbitMQConfig  `mapstructure:"rabbitmq"`
	*JWTConfig       `mapstructure:"jwt"`
	*SnowflakeConfig `mapstructure:"snowflake"`
	*UploadConfig    `mapstructure:"upload"`
}

// UploadConfig 上传文件配置：本地磁盘存储（MVP 不引入对象存储）。
// Dir 在两种部署形态下不同——本地 dev 相对 backend/，容器内挂命名卷，
// 由各环境配置文件自行指定，代码不写死路径。
type UploadConfig struct {
	Dir       string `mapstructure:"dir"`         // 上传根目录（头像存 {dir}/avatars/）
	MaxSizeMB int64  `mapstructure:"max_size_mb"` // 单文件大小上限（MB）
}

type MySQLConfig struct {
	Host         string `mapstructure:"host"`
	Username     string `mapstructure:"username"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	Charset      string `mapstructure:"charset"`
	Loc          string `mapstructure:"loc"`
	Port         int    `mapstructure:"port"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime string `mapstructure:"conn_max_lifetime"` // 单条连接最长存活时间（如 "1h"），防止被 MySQL wait_timeout 悄悄杀掉
	ParseTime       bool   `mapstructure:"parse_time"`
}

type RedisConfig struct {
	Host         string `mapstructure:"host"`
	Password     string `mapstructure:"password"`
	Port         int    `mapstructure:"port"`
	DB           int    `mapstructure:"db"`
	PoolSize     int    `mapstructure:"pool_size"`
	MinIdleConns int    `mapstructure:"min_idle_conns"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxAge     int    `mapstructure:"max_age"`
	MaxBackups int    `mapstructure:"max_backups"`
}

type RabbitMQConfig struct {
	Port     int    `mapstructure:"port"`
	Host     string `mapstructure:"host"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Vhost    string `mapstructure:"vhost"`
	Exchange string `mapstructure:"exchange"`
}

type JWTConfig struct {
	Secret string        `mapstructure:"secret"`
	Expire time.Duration `mapstructure:"expire"`
	Issuer string        `mapstructure:"issuer"`
}

type SnowflakeConfig struct {
	StartTime    string `mapstructure:"start_time"`
	DatacenterID int64  `mapstructure:"datacenter_id"`
	WorkerID     int64  `mapstructure:"worker_id"`
}
