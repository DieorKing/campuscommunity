package conf

import "time"

var Conf = new(ServerConfig)

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
}

type MySQLConfig struct {
	Host         string `mapstructure:"host"`
	Username     string `mapstructure:"username"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	Charset      string `mapstructure:"charset"`
	Loc          string `mapstructure:"loc"`
	Port         int    `mapstructure:"port"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	ParseTime    bool   `mapstructure:"parse_time"`
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
