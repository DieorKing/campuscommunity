// Package mq RabbitMQ 数据访问层：连接管理、拓扑声明、消息发布与消费。
// 本文件为消费侧基础设施：消费者注册表 + 通用消费循环。
//
// 设计：mq 包对业务零感知——不解析消息体、不 import 业务包（依赖倒置，
// 见 OrderCreator 的演进注释）。各业务消费者以 handler 函数注册进来：
//
//	mq.Register(queue, routingKey, handler)   // main 装配时调用
//	mq.RunConsumers()                          // 启动全部已注册消费者
//
// handler 契约：入参是原始消息体（[]byte），解析是 handler 自己的事；
// 返回值表达处置意图——
//
//	nil / errors.Is(err, mq.ErrAck) → ack（处理成功或确定性失败，如重复/畸形）
//	其他 error                      → nack + 指数退避重投（暂时性故障）
package mq

import (
	"errors"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// ErrAck 确定性失败哨兵：handler 返回包裹它的错误表示「这条消息到此为止，
// ack 丢弃不要再投」。典型场景：重复消息撞唯一索引、畸形 JSON、目标不存在
// ——重投一万次也不会成功，nack 只会制造毒消息。
// 用法：return fmt.Errorf("duplicate order: %w", mq.ErrAck)
var ErrAck = errors.New("mq: ack this message (deterministic failure)")

// retryBackoffBase / retryBackoffMax 暂时性失败的退避参数：
// 1s 起步指数翻倍，30s 封顶。封顶保证故障恢复后最长 30s 内重回工作状态。
const (
	retryBackoffBase = 1 * time.Second
	retryBackoffMax  = 30 * time.Second
)

// consumer 一个已注册消费者的全部静态描述：消费哪个队列、消息经哪条路由来、
// 由哪个 handler 处理。queue 与 routingKey 一一对应（direct 交换机）。
type consumer struct {
	queue      string
	routingKey string
	handler    Handler
}

// Handler 业务消费者的处理函数类型。raw body 由消费循环原样传入——
// 解析（JSON 反序列化）是 handler 的职责，mq 包不认识消息结构。
type Handler func(body []byte) error

// consumers 注册表。读写时序上先 Register 后 RunConsumers（main 顺序保证），
// 实际无并发写；RWMutex 是防御性的——把「注册期」与「消费期」的边界
// 显式锁住，未来谁在消费期动态注册也不会 data race。
var (
	consumersMu sync.RWMutex
	consumers   = make(map[string]*consumer) // key: queue 名
)

// Register 注册一个消费者（幂等防御：同名队列重复注册视为编码错误，fail-fast）。
// 必须在 RunConsumers 之前调用（main 装配阶段）。
func Register(queue, routingKey string, h Handler) {
	consumersMu.Lock()
	defer consumersMu.Unlock()
	if _, ok := consumers[queue]; ok {
		panic("mq: consumer already registered for queue " + queue)
	}
	if h == nil {
		panic("mq: nil handler for queue " + queue)
	}
	consumers[queue] = &consumer{queue: queue, routingKey: routingKey, handler: h}
}

// RunConsumers 启动全部已注册消费者（每个一条 goroutine 长驻消费循环）。
// main 在 mq.Init 之后调用一次。
func RunConsumers() {
	consumersMu.RLock()
	defer consumersMu.RUnlock()
	if len(consumers) == 0 {
		zap.L().Warn("mq: no consumers registered, RunConsumers is a no-op")
		return
	}
	for _, c := range consumers {
		// 闭包捕获当前 consumer（Go 经典坑：range 变量复用，必须传参拷贝）
		go consumeLoop(*c)
	}
	zap.L().Info("mq: all consumers started", zap.Int("count", len(consumers)))
}

// consumeLoop 单个消费者的长驻循环：独立 channel（与发布分离）→ QoS=1 →
// 手动 ack → 逐条处理。channel 断连（broker 重启等）时循环退出由上层感知；
// 生产环境应加重连机制，MVP 随进程重启。
func consumeLoop(c consumer) {
	// 防御性检查：连接未初始化即启动 = 装配时序错误
	if conn == nil {
		zap.L().Fatal("mq: consume loop: connection not initialized (call mq.Init first)")
		return
	}
	// 独立 channel：消费与发布分用不同 channel（amqp091 锁粒度是 channel 级，
	// 分离可避免消费回调阻塞发布路径）
	ch, err := conn.Channel()
	if err != nil {
		zap.L().Fatal("mq: consume loop open channel", zap.String("queue", c.queue), zap.Error(err))
		return
	}
	// prefetch=1：一次只预取一条——防积压消息灌爆消费者内存，
	// 且失败时手里只有一条悬空消息
	if err := ch.Qos(1, 0, false); err != nil {
		zap.L().Fatal("mq: consume loop set qos", zap.String("queue", c.queue), zap.Error(err))
		return
	}
	// autoAck=false：手动确认——处理中途崩溃消息不丢（broker 重投，
	// 消费端靠业务幂等兜住重复）
	deliveries, err := ch.Consume(
		c.queue, // 队列：Init 拓扑声明的名字
		"",      // consumer tag：空 = broker 自动生成
		false,   // autoAck=false：手动 ack
		false,   // exclusive：断连后队列不删
		false,   // noLocal：不屏蔽自己发布的消息
		false,   // noWait：等 broker 确认订阅
		nil,
	)
	if err != nil {
		zap.L().Fatal("mq: consume loop subscribe", zap.String("queue", c.queue), zap.Error(err))
		return
	}
	zap.L().Info("mq: consumer started", zap.String("queue", c.queue))

	// 每消费者独立的退避计数：多消费者并发失败时互不污染对方的节奏
	backoff := newBackoff()

	for d := range deliveries {
		dispatch(c, d, backoff)
	}
	// deliveries 关闭（channel 断连/进程关闭）：range 结束退出
	zap.L().Warn("mq: consumer channel closed, loop exited", zap.String("queue", c.queue))
}

// dispatch 单条消息的统一分派：handler 执行 + 错误分类（ack / nack退避）。
// 失败分类契约见包注释：nil 或 ErrAck 系 → ack；其他 → nack + 指数退避。
func dispatch(c consumer, d amqp.Delivery, b *backoff) {
	err := c.handler(d.Body)

	switch {
	case err == nil:
		// 处理成功。error 级业务日志归 handler 自己打（它知道业务上下文），
		// 这里只留一条 debug 级轨迹
		ack(d)
	case errors.Is(err, ErrAck):
		// 确定性失败：重投无意义，ack 出局（防毒消息——防线在 handler 的分类）
		zap.L().Info("mq: message acked as deterministic failure",
			zap.String("queue", c.queue), zap.Uint64("delivery_tag", d.DeliveryTag),
			zap.Error(err))
		ack(d)
	default:
		// 暂时性失败：nack 重投 + 指数退避。退避让恢复中的基础设施喘口气
		//（狂重试等于二次攻击）；prefetch=1 保证退避期间不堆积预取消息。
		// requeue=true 显式写：false 会把消息死信或丢弃，本拓扑无 DLX = 直接丢
		wait := b.next()
		zap.L().Error("mq: transient failure, nack with backoff",
			zap.String("queue", c.queue), zap.Uint64("delivery_tag", d.DeliveryTag),
			zap.Duration("backoff", wait), zap.Error(err))
		time.Sleep(wait)
		nack(d)
	}
}

// ack 确认消息。失败仅记日志：broker 未收到 ack 会重投该消息，
// 消费端业务幂等（唯一索引等）保证重投安全。
func ack(d amqp.Delivery) {
	if err := d.Ack(false); err != nil {
		zap.L().Error("mq: ack failed (broker will redeliver)",
			zap.Uint64("delivery_tag", d.DeliveryTag), zap.Error(err))
	}
}

// nack 否认并重回队列（requeue=true 显式；multiple=false 单条）。
func nack(d amqp.Delivery) {
	if err := d.Nack(false, true); err != nil {
		zap.L().Error("mq: nack failed (broker will redeliver)",
			zap.Uint64("delivery_tag", d.DeliveryTag), zap.Error(err))
	}
}

// backoff 指数退避计数器（每消费者一个实例）。
// 消费成功后 reset 归零：连续失败序列被打断，下次故障从 1s 重新起跳
// （长时间稳定运行后偶发一次故障，不应继承历史积累的长退避）。
// 无锁：一个 backoff 只被一个消费循环的 goroutine 碰。
type backoff struct {
	failures int
}

func newBackoff() *backoff { return &backoff{} }

// next 计算下一次退避时长并推进失败计数。
// 1s << (failures-1)：1s→2s→4s→...；位移在 failures 很大时溢出为负，
// 「超上限或非正」双条件兜回 30s 封顶。
func (b *backoff) next() time.Duration {
	b.failures++
	wait := retryBackoffBase << (b.failures - 1)
	if wait > retryBackoffMax || wait <= 0 {
		wait = retryBackoffMax
	}
	return wait
}

// reset 消费成功（含确定性失败 ack）后调用：归零退避序列。
func (b *backoff) reset() {
	b.failures = 0
}
