package mq

import (
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// GrabOrderMessage 建单消息体：仅两个业务主键。
// 为什么不带更多字段（price/address 等）：消费者建单时自行回表取值，
// 消息是"事件通知"（谁抢了哪个拼单）而非"数据载体"——
// 消息体最小化降低序列化/网络/存储成本，且杜绝消息内携带过期快照。
type GrabOrderMessage struct {
	GoodID int64 `json:"good_id"`
	UserID int64 `json:"user_id"`
}

// NotificationMessage 通知消息体：一封信的投递单（收件人 + 信的内容）。
// 标题正文由业务侧（logic）组装好直接放消息——通知消费者是纯管道
// （解析→INSERT），不做任何业务计算，内容组装在事件发生的现场
// （那时上下文最全，如订单金额、拼单标题都已在手）。
// 幂等键：消费端依赖 (UserID+Category+RefID) 的 DB 唯一索引，
// 消息本身不带 msg_id——唯一索引就是去重事实源。
type NotificationMessage struct {
	UserID   int64  `json:"user_id"`  // 收件人
	Type     string `json:"type"`     // 大类：group_buy / order
	Category string `json:"category"` // 细类：pending_pay / paid / ...
	RefID    int64  `json:"ref_id"`   // 关联业务键（good_id 或 order_id）
	Title    string `json:"title"`    // 通知标题
	Content  string `json:"content"`  // 通知正文
}

// PublishGrabOrder 发布建单消息（在分布式锁内调用）。
// DeliveryMode=Persistent + durable 队列：消息落盘，broker 重启不丢。
// 失败语义：本函数只如实返回 err，不做任何业务决策；「投递失败后预扣是否回滚、
// 是否照常返回受理」这类决策由 logic 层做出（分层失败策略——DAO/MQ 层不吞错、不做业务决策）。
// producer confirm（发布确认）属后续 MQ 基建加固，当前用 Publish 返回值
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

// PublishNotification 发布通知消息（业务 logic 的事件尾部调用，best-effort）。
// 与建单消息的失败语义差异：通知是易失展示数据——logic 层调用失败仅记日志，
// 不回滚不重试（挂尾部天然免重投之害，详见 logic 挂载点注释）。
// 标题正文已由调用方组装完毕，本函数只负责序列化与投递。
func PublishNotification(msg NotificationMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mq: marshal notification message: %w", err)
	}
	err = channel.Publish(
		exchangeName,
		NotifyRoutingKey,
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("mq: publish notification: %w", err)
	}
	return nil
}
