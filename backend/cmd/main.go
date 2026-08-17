package main

import (
	"campuscommunity/internal/conf"
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/router"
	"campuscommunity/pkg/utils/logger"
	"campuscommunity/pkg/utils/snowflake"
	"flag"
	"fmt"
)

func main() {
	// 配置文件路径：相对于运行时工作目录(cwd)。
	// 约定在 backend/ 目录执行 `go run cmd/main.go`，故路径为 internal/conf/config.yaml。
	filename := flag.String("f", "internal/conf/config.yaml", "配置文件路径(相对于运行时cwd)")
	// 加载配置
	if err := conf.Init(*filename); err != nil {
		fmt.Printf("load config failed, err:%v\n", err)
		return
	}
	//初始化日志
	if err := logger.Init(conf.Conf.LogConfig, conf.Conf.Mode); err != nil {
		fmt.Printf("init logger failed, err:%v\n", err)
		return
	}
	//初始化雪花算法
	if err := snowflake.Init(conf.Conf.StartTime, conf.Conf.DatacenterID, conf.Conf.WorkerID); err != nil {
		fmt.Printf("init snowflake failed, err:%v\n", err)
		return
	}
	//初始化mysql
	if err := mysql.Init(conf.Conf.MySQLConfig); err != nil {
		fmt.Printf("init mysql failed, err:%v\n", err)
		return
	}
	defer mysql.Close() // 程序退出关闭数据库连接
	// 自动建表：5 张业务表（阶段1）
	if err := mysql.Migration(); err != nil {
		fmt.Printf("mysql migration failed, err:%v\n", err)
		return
	}
	//初始化redis
	if err := redis.Init(conf.Conf.RedisConfig); err != nil {
		fmt.Printf("init redis failed, err:%v\n", err)
		return
	}
	defer redis.Close()
	// 注册路由
	r := router.SetupRouter(conf.Conf.Mode)
	err := r.Run(fmt.Sprintf(":%d", conf.Conf.Port))
	if err != nil {
		fmt.Printf("run server failed, err:%v\n", err)
		return
	}
}
