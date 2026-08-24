// Package mq RabbitMQ 数据访问层：连接管理、拓扑声明、消息发布与消费。
// 本文件为建单消费者（建单七步流程中的第 7 步）：
// 接收建单消息 → 调 logic 编排 → 按失败分类执行 ack/nack。
// 分层约束：本包不懂业务（不查库、不装配订单），业务全在 logic；
// 本包只负责三件事——收消息、传消息、按错误身份回话（ack/nack）。
package mq

import (
	"encoding/json"
	"errors"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// OrderCreator 建单编排的依赖注入接口（依赖倒置解 import cycle）。
// 问题背景：消费者要调 logic 编排，而 logic 的抢单流程要调 mq 发布——
// mq → logic 与 logic → mq 同向并存 = Go 不允许的 import cycle。
// 解法：mq 包不 import logic，而是定义本函数类型，由 main 装配时把
// logic.CreateOrderByMessage 注入进来——依赖方向从「mq 依赖 logic」
// 反转为「main 依赖双方、mq 只依赖抽象」。mq 包从此对业务层零感知。
type OrderCreator func(goodID, userID int64) error

// createOrder 建单编排函数变量：ConsumeGrabOrder 启动时由 main 注入
// （非导出的 setter + 包级变量，保证初始化时序受控：先注入再消费）。
var createOrder OrderCreator

// BindOrderCreator 绑定建单编排函数（main.go 在 mq.Init 之后、
// ConsumeGrabOrder 之前调用；重复绑定视为编码错误直接 panic fail-fast）。
func BindOrderCreator(fn OrderCreator) {
	if createOrder != nil {
		panic("mq: order creator already bound")
	}
	createOrder = fn
}

// 消费者侧的哨兵错误：与 logic 的哨兵一一对应但由 logic 包定义、
// mq 包不 import logic（见 OrderCreator 注释）——错误经 error 接口
// 传递，判别仍用 errors.Is。这里定义「约定字符串」由 logic 层在
// 注入时转译（见 main.go 的适配函数：把 dao.ErrDuplicateEntry 翻译
// 为本包可识别的形态）。
var (
	// ErrDuplicateOrder 重复消息的成功证明（订单已建，直接 ack）。
	ErrDuplicateOrder = errors.New("mq: duplicate grab order message")
	// ErrOrderTargetNotExist good 或 user 不存在（确定性失败，ack + error 日志）。
	ErrOrderTargetNotExist = errors.New("mq: grab order target not exist")
)

// 退避序列 1s→2s→4s→8s→...→30s 封顶（指数退避，Exponential Backoff）：
// 每次失败后等待时长翻倍，防止打爆恢复中的 DB/Redis（故障期疯狂重试
// 等于二次攻击）；封顶 30s 保证恢复后最长 30s 内重回工作状态。
const (
	retryBackoffBase = 1 * time.Second
	retryBackoffMax  = 30 * time.Second
)

// ConsumeGrabOrder 启动建单消费者（main.go 在 mq.Init 之后调用，阻塞式长驻）。
// 应放入独立 goroutine 运行：Consume 的 deliveries channel 阻塞等待投递，
// 不占用 HTTP 主协程。
//
// 消费模型三件套（缺一不可，各自防一种事故）：
//   - QoS prefetch=1：一次只从 broker 预取一条消息——防止积压消息一股脑
//     推进消费者内存（打爆内存 + 失败时手里攥着一堆悬空消息）；
//   - 手动 ack（autoAck=false）：处理完才确认——处理中途崩溃消息不丢
//     （broker 未收到 ack，重启后重投；消费端靠唯一索引幂等兜住重复）；
//   - Consume 返回的 channel 断连时 Return 退出：上层（main）感知后重启。
func ConsumeGrabOrder() {
	// 防御性检查：conn 为 nil 说明 Init 未被调用，或 := 变量遮蔽问题复发
	// （历史上曾因 := 遮蔽导致包级 conn 恒为 nil，消费者首个
	// conn.Channel() 调用即 panic）。Fatal 而非 return：连接缺失
	// 属启动级故障，fail-fast 交给运维重启。
	if conn == nil {
		zap.L().Fatal("mq: consume grab order: connection not initialized (call mq.Init first)")
		return
	}
	// 独立 channel：消费与发布分用不同 channel（amqp091 的锁粒度是 channel 级，
	// 分离可避免消费回调阻塞发布路径）。
	ch, err := conn.Channel()
	if err != nil {
		// 消费者起不来属于启动级故障：fail-fast，进程退出交由运维重启
		zap.L().Fatal("mq: consume grab order open channel", zap.Error(err))
		return
	}
	// prefetch=1：公平派发 + 内存安全（见函数头注释三件套之一）
	if err := ch.Qos(1, 0, false); err != nil {
		zap.L().Fatal("mq: consume grab order set qos", zap.Error(err))
		return
	}
	// autoAck=false：手动确认模式（三件套之二）
	deliveries, err := ch.Consume(
		GrabOrderQueue, // 队列：与生产者共同的"地址协议"
		"",             // consumer tag：空 = broker 自动生成
		false,          // autoAck=false：手动 ack
		false,          // exclusive=false：断连后队列不删除
		false,          // noLocal=false：不屏蔽自己发布的消息
		false,          // noWait=false：等待 broker 确认订阅成功
		nil,            // args：无额外参数
	)
	if err != nil {
		zap.L().Fatal("mq: consume grab order", zap.Error(err))
		return
	}
	zap.L().Info("mq: grab order consumer started")

	// 阻塞循环：逐条取消息（prefetch=1 决定了天然逐条）
	for d := range deliveries {
		handleGrabOrder(ch, d)
	}
	// channel 关闭（broker 断连/进程关闭）：range 结束退出，由上层重启
	zap.L().Warn("mq: grab order consumer channel closed, deliveries loop exited")
}

// handleGrabOrder 单条消息的完整处理：解析 → 编排 → 失败分类 → ack/nack。
// 失败分类策略：确定性失败一律 ack（毒消息防御在分类层），
// 暂时性失败 nack 重投 + sleep 指数退避（故障注定恢复，无界重试安全）。
func handleGrabOrder(ch *amqp.Channel, d amqp.Delivery) {
	// ---- 第 1 步：解析消息体 ----
	var msg GrabOrderMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		// 畸形 JSON = 确定性失败：重投一万次也解析不了 → ack 出局。
		// 「丢失」的正确姿势：原始消息体打进 error 日志——日志即轻量
		// DLQ，可追溯可重放；同时记 deliveryTag
		// 便于运维在管理台按 tag 对账。
		zap.L().Error("mq: grab order message malformed, acked and archived to log",
			zap.Uint64("delivery_tag", d.DeliveryTag),
			zap.ByteString("body", d.Body), zap.Error(err))
		ack(ch, d)
		return
	}

	// 消费成功（含重复消息）后重置退避计数：连续失败序列被打断，
	// 下次故障从 1s 重新起跳（长时间稳定运行后偶发一次故障，
	// 不应继承历史上积累的长退避）
	defer func() { backoffSeq.failures = 0 }()

	// ---- 第 2 步：交给注入的编排函数（业务全在 logic 层，见 OrderCreator） ----
	// 未绑定即消费 = 启动时序错误（main 必须先 BindOrderCreator），防御性退出
	if createOrder == nil {
		zap.L().Fatal("mq: order creator not bound, call BindOrderCreator before consuming")
		return
	}
	err := createOrder(msg.GoodID, msg.UserID)

	// ---- 第 3 步：失败分类 → ack / nack（四类错误身份） ----
	switch {
	case err == nil:
		// 身份 3：正常建单成功（日志埋点：成团/满员触发者与计数语义，
		// 成团通知的挂载点在这里）
		zap.L().Info("mq: grab order consumed",
			zap.Int64("good_id", msg.GoodID), zap.Int64("user_id", msg.UserID))
		ack(ch, d)

	case errors.Is(err, ErrDuplicateOrder):
		// 身份 1：重复消息的成功证明——订单已建，本条是 at-least-once
		// 的正常代价。直接 ack，绝不 nack（nack = 无限撞索引 = 毒消息）。
		zap.L().Info("mq: duplicate grab order message acked (idempotent)",
			zap.Int64("good_id", msg.GoodID), zap.Int64("user_id", msg.UserID))
		ack(ch, d)

	case errors.Is(err, ErrOrderTargetNotExist):
		// 身份 2：确定性失败（good/user 已不存在）——重投无意义 → ack。
		// error 级日志：这是数据异常信号（生产者发消息时存在、消费时被删），
		// 值得人工关注，但不需要重试。
		zap.L().Error("mq: grab order target not exist, acked",
			zap.Int64("good_id", msg.GoodID), zap.Int64("user_id", msg.UserID),
			zap.Error(err))
		ack(ch, d)

	default:
		// 身份 4：暂时性失败（DB 断连/超时等）——唯一允许 nack 的分支。
		// sleep 指数退避后再 nack：退避让「恢复中的基础设施」喘口气，
		// 也让本协程在退避期间不空转抢消息（prefetch=1：手里只有这一条，
		// sleep 天然不会造成消息堆积在消费者侧）。
		// requeue=true 必须显式写：false 会把消息
		// 死信或丢弃，本拓扑无 DLX，等于直接丢消息。
		backoff := retryBackoff()
		zap.L().Error("mq: grab order transient failure, nack with backoff",
			zap.Int64("good_id", msg.GoodID), zap.Int64("user_id", msg.UserID),
			zap.Duration("backoff", backoff), zap.Error(err))
		time.Sleep(backoff)
		nack(ch, d)
	}
}

// ack 确认消息（含错误防御：ack 失败意味着 channel 已断，只记日志——
// broker 未收到 ack 会重投该消息，消费端幂等（唯一索引）保证安全）。
func ack(ch *amqp.Channel, d amqp.Delivery) {
	if err := d.Ack(false); err != nil {
		zap.L().Error("mq: ack grab order failed (broker will redeliver)",
			zap.Uint64("delivery_tag", d.DeliveryTag), zap.Error(err))
	}
}

// nack 否认消息并重回队列（requeue=true 显式写；multiple=false 单条处理）。
func nack(ch *amqp.Channel, d amqp.Delivery) {
	if err := d.Nack(false, true); err != nil {
		zap.L().Error("mq: nack grab order failed (broker will redeliver)",
			zap.Uint64("delivery_tag", d.DeliveryTag), zap.Error(err))
	}
}

// backoffSeq 退避序列状态：包级单例——同一时刻只有一个消费者协程在跑
// （ConsumeGrabOrder 只被调用一次），无并发竞争；重试间隔随连续失败
// 次数翻倍，消费成功后由 handleGrabOrder 的 defer 重置归零。
// 退避计数用「内存状态 + 日志」实现（无重试上限、
// 无持久化——重试计数防的是分类错了，我们的分类是对的）。
var backoffSeq struct {
	failures int
}

// retryBackoff 计算下一次退避时长并推进失败计数。
// 返回值由调用方记日志，保持单一职责。
func retryBackoff() time.Duration {
	backoffSeq.failures++
	// 1s << (failures-1)：1s→2s→4s→...，位移翻倍；封顶 30s。
	// failures 很大时 1s<<n 会溢出（int64 上限），溢出后为负数，
	// 用「超上限或非正」双条件兜回 30s。
	backoff := retryBackoffBase << (backoffSeq.failures - 1)
	if backoff > retryBackoffMax || backoff <= 0 {
		backoff = retryBackoffMax
	}
	return backoff
}
