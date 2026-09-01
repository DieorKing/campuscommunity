package main

import (
	"encoding/json"

	"campuscommunity/internal/conf"
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/logic"
	"campuscommunity/internal/model"
	"campuscommunity/internal/mq"
	"campuscommunity/internal/router"
	"campuscommunity/pkg/utils/logger"
	"campuscommunity/pkg/utils/snowflake"
	"errors"
	"flag"
	"fmt"

	"go.uber.org/zap"
)

// grabOrderHandler 建单消费者的适配函数：解析消息体 → 调 logic 编排 →
// 错误分类翻译。mq 包对业务零感知（解 import cycle 的依赖倒置），
// main 是唯一同时看见 mq 协议与 logic 业务的地方，翻译职责天然在此。
// 错误契约：nil=成功；包裹 mq.ErrAck=确定性失败（ack 丢弃，防毒消息）；
// 其他=暂时性失败（消费循环 nack + 指数退避重投）。
func grabOrderHandler(body []byte) error {
	// 1. 解析消息：畸形 JSON 属确定性失败——重投一万次也解析不了。
	// 原始消息体打进 error 日志（可追溯可重放的轻量 DLQ）
	var msg mq.GrabOrderMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		zap.L().Error("grab order message malformed, acked",
			zap.ByteString("body", body), zap.Error(err))
		return errors.Join(fmt.Errorf("malformed message: %w", err), mq.ErrAck)
	}

	// 2. 业务编排
	_, err := logic.CreateOrderByMessage(msg.GoodID, msg.UserID)
	switch {
	case err == nil:
		return nil
	// 撞唯一索引 = 重复消息的成功证明：订单已建，直接 ack（nack = 无限撞索引 = 毒消息）
	case errors.Is(err, dao.ErrDuplicateEntry):
		zap.L().Info("duplicate grab order message acked (idempotent)",
			zap.Int64("good_id", msg.GoodID), zap.Int64("user_id", msg.UserID))
		return errors.Join(fmt.Errorf("duplicate order: %w", err), mq.ErrAck)
	// good/user 不存在 = 确定性失败：数据异常信号值得人工关注，但重投无意义
	case errors.Is(err, logic.ErrGoodNotExist), errors.Is(err, logic.ErrUserNotExist):
		zap.L().Error("grab order target not exist, acked",
			zap.Int64("good_id", msg.GoodID), zap.Int64("user_id", msg.UserID), zap.Error(err))
		return errors.Join(fmt.Errorf("target not exist: %w", err), mq.ErrAck)
	default:
		// 暂时性失败（DB 断连/超时）：透传，消费循环 nack 退避重投
		return err
	}
}

// notificationHandler 通知消费者的适配函数：解析 → 组装通知行 → 落库。
// 消费者是纯管道（无业务计算，内容在事件现场组装完毕）——本函数只做
// 协议翻译：消息结构 → 表行结构（雪花 ID 生成）。
// 错误契约：重复事件（撞 uk_user_category_ref 唯一索引）静默 ack——
// at-least-once 的正常代价，不打日志防刷屏；畸形消息 ack + error 日志留痕；
// DB 故障透传走 nack 退避。
func notificationHandler(body []byte) error {
	var msg mq.NotificationMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		zap.L().Error("notification message malformed, acked",
			zap.ByteString("body", body), zap.Error(err))
		return errors.Join(fmt.Errorf("malformed notification: %w", err), mq.ErrAck)
	}
	// 字段完整性防御：缺 user_id/ref_id 的消息无法落库（非空列），
	// 属生产端 bug 的信号，记日志 ack（确定性失败）
	if msg.UserID <= 0 || msg.RefID <= 0 {
		zap.L().Error("notification message missing key field, acked",
			zap.Int64("user_id", msg.UserID), zap.Int64("ref_id", msg.RefID))
		return errors.Join(fmt.Errorf("notification missing field"), mq.ErrAck)
	}

	n := &model.Notification{
		NotificationID: model.ID(snowflake.GenID()),
		UserID:         model.ID(msg.UserID),
		Type:           model.NotificationType(msg.Type),
		Category:       model.NotificationCategory(msg.Category),
		Title:          msg.Title,
		Content:        msg.Content,
		RefID:          model.ID(msg.RefID),
	}
	err := dao.CreateNotification(n)
	switch {
	case err == nil:
		return nil
	// 重复事件：唯一索引已拦，静默 ack（通知幂等防线的物理层）
	case errors.Is(err, dao.ErrNotificationDuplicate):
		return errors.Join(fmt.Errorf("duplicate notification"), mq.ErrAck)
	default:
		return err // DB 故障：nack 退避重投
	}
}

func main() {
	// 配置文件路径：相对于运行时工作目录(cwd)。
	// 约定在 backend/ 目录执行 `go run cmd/main.go`，故路径为 internal/conf/config.yaml。
	filename := flag.String("f", "internal/conf/config.yaml", "配置文件路径(相对于运行时cwd)")
	flag.Parse() // 解析命令行参数；不调用则 -f 参数被忽略，恒用默认路径
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
	// 注册消费者（依赖注入：mq 包对业务零感知，handler 在装配点绑定）
	mq.Register(mq.GrabOrderQueue, mq.GrabOrderRoutingKey, grabOrderHandler)
	mq.Register(mq.NotifyQueue, mq.NotifyRoutingKey, notificationHandler)
	// 启动全部已注册消费者：每消费者一条 goroutine 长驻
	// （QoS=1 + 手动 ack + 失败分类退避，见 mq/consumer.go）
	mq.RunConsumers()
	// 启动延时任务扫描器：订单超时关闭 + 拼单截止判定
	// （10s 一轮独立 goroutine，随进程退出；未处理任务留在 ZSet，重启补扫）
	go logic.StartDelayScanner()
	// 补偿扫描器：10s 轮捞 delay:compensation:retry 到期任务，
	// 幂等执行补偿动作（成功删行/失败退避/5 次封顶翻 failed 终态）
	go logic.StartCompensationScanner()
	// pending 对账扫描器：10s 捞 pending:grab 滞留超时标记（=建单消息
	// 丢失，无单点报错只能靠标记发现），重发消息（幂等兜底）+ 续期
	go logic.StartPendingScanner()
	// 注册路由
	r := router.SetupRouter(conf.Conf.Mode)
	err := r.Run(fmt.Sprintf(":%d", conf.Conf.Port))
	if err != nil {
		fmt.Printf("run server failed, err:%v\n", err)
		return
	}
}
