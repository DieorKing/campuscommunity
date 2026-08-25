package main

import (
	"campuscommunity/internal/conf"
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/logic"
	"campuscommunity/internal/mq"
	"campuscommunity/internal/router"
	"campuscommunity/pkg/utils/logger"
	"campuscommunity/pkg/utils/snowflake"
	"errors"
	"flag"
	"fmt"
)

// adaptOrderCreator 把 logic.CreateOrderByMessage 包装为 mq.OrderCreator，
// 并把 logic/dao 的哨兵错误翻译为 mq 包的消费者哨兵（解 import cycle 的
// 适配层：mq 不 import logic，错误翻译在装配点完成——main 是唯一
// 同时看见两个业务包的地方，转译职责天然属于它）。
// 返回值约定：nil=建单成功；mq.ErrDuplicateOrder=重复消息；
// mq.ErrOrderTargetNotExist=good/user 不存在；其他原样透传（暂时性失败）。
func adaptOrderCreator(goodID, userID int64) error {
	_, err := logic.CreateOrderByMessage(goodID, userID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dao.ErrDuplicateEntry):
		return mq.ErrDuplicateOrder
	case errors.Is(err, logic.ErrGoodNotExist), errors.Is(err, logic.ErrUserNotExist):
		return mq.ErrOrderTargetNotExist
	default:
		return err
	}
}

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
	// 自动建表：5 张业务表
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
	//初始化RabbitMQ（含交换机/队列/绑定拓扑声明，幂等）
	if err := mq.Init(conf.Conf.RabbitMQConfig); err != nil {
		fmt.Printf("init rabbitmq failed, err:%v\n", err)
		return
	}
	defer mq.Close()
	// 绑定建单编排（依赖注入：解 mq→logic 与 logic→mq 的 import cycle，
	// 适配函数同时完成哨兵错误转译，见 adaptOrderCreator 注释）
	mq.BindOrderCreator(adaptOrderCreator)
	// 启动建单消费者：独立 goroutine 长驻——Consume 阻塞等投递，
	// 不能占用 HTTP 主协程；QoS=1 + 手动 ack + 失败分类见 consumer_order.go
	go mq.ConsumeGrabOrder()
	// 启动延时任务扫描器：订单超时关闭 + 拼单截止判定
	// （10s 一轮独立 goroutine，随进程退出；未处理任务留在 ZSet，重启补扫）
	go logic.StartDelayScanner()
	// 注册路由
	r := router.SetupRouter(conf.Conf.Mode)
	err := r.Run(fmt.Sprintf(":%d", conf.Conf.Port))
	if err != nil {
		fmt.Printf("run server failed, err:%v\n", err)
		return
	}
}
