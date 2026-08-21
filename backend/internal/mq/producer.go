package mq

import (
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// GrabOrderMessage 建单消息体（契约 mvp §4.3 step4）：仅两个业务主键。
// 为什么不带更多字段（price/address 等）：消费者建单时自行回表取值，
// 消息是"事件通知"（谁抢了哪个拼单）而非"数据载体"——
// 消息体最小化降低序列化/网络/存储成本，且杜绝消息内携带过期快照。
type GrabOrderMessage struct {
	GoodID int64 `json:"good_id"`
	UserID int64 `json:"user_id"`
}

// PublishGrabOrder 发布建单消息（契约 mvp §4.3 step4，在分布式锁内调用）。
// DeliveryMode=Persistent + durable 队列：消息落盘，broker 重启不丢。
// 失败语义：本函数只如实返回 err，不做任何业务决策；「投递失败后预扣是否回滚、
// 是否照常返回受理」这类决策由 logic 层做出（分层失败策略 mvp §9——DAO/MQ 层不吞错、不做业务决策）。
// producer confirm（发布确认）属阶段7 MQ 基建加固，当前用 Publish 返回值
// 做基本错误捕获（连接断开等同步可发现的错误）。
func PublishGrabOrder(goodID, userID int64) error {
	// 消息体序列化：JSON 结构化，消费端按同结构反序列化（两端共享消息定义）
	body, err := json.Marshal(GrabOrderMessage{GoodID: goodID, UserID: userID})
	if err != nil {
		return fmt.Errorf("mq: marshal grab order message: %w", err)
	}
	// 发布到交换机 + 路由键；mandatory=false：路由无目的地时不退回（拓扑已在 Init 保证存在）
	err = channel.Publish(
		exchangeName,
		GrabOrderRoutingKey,
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent, // 持久化消息（broker 落盘）
			ContentType:  "application/json",
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("mq: publish grab order: %w", err)
	}
	return nil
}
