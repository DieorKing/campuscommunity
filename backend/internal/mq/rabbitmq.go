// Package mq RabbitMQ 数据访问层：连接管理、拓扑声明与消息发布，不含业务逻辑。
// 本文件为连接与拓扑管理；消息发布见 producer.go，消费见 consumer_order.go。
package mq

import (
	"fmt"

	"campuscommunity/internal/conf"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	conn    *amqp.Connection
	channel *amqp.Channel
	// exchangeName 发布用的交换机名，Init 时从配置读取缓存（发布路径不重复读配置）
	exchangeName string
)

// 建单消息的拓扑常量（生产者与消费者共同的"地址协议"，两端必须一致）：
// exchange = campus.community（config.yaml），queue = campus.community.grab_order，
// routing key = grab_order。direct 交换机按 key 精确匹配路由。
const (
	// GrabOrderQueue 建单消息队列名
	GrabOrderQueue = "campus.community.grab_order"
	// GrabOrderRoutingKey 建单消息路由键
	GrabOrderRoutingKey = "grab_order"
)

// Init 初始化 RabbitMQ 连接与拓扑（fail-fast：连接失败直接 fatal，不降级运行）。
// 拓扑声明（exchange/queue/binding）幂等：重复声明参数一致无副作用，
// 等价于 MySQL AutoMigrate 的"启动时确保结构存在"——消费者重复声明不冲突。
// durable=true + persistent 消息：broker 重启后拓扑与未消费消息不丢（防丢消息三段防御的 broker 侧）。
func Init(cfg *conf.RabbitMQConfig) error {
	// 建连：amqp://user:pass@host:port/vhost
	// vhost 为 "/" 时 URL 以 / 结尾（默认 vhost），与 config.yaml 一致
	// ! 赋值必须用 = 而非 :=：若写成 conn, err := amqp.Dial(url)，:= 会声明
	// 【函数局部】conn 遮蔽（shadow）包级 conn——局部 conn 拿到真连接，包级
	// conn 永远为 nil，Init 却照样返回成功；直到首个使用包级 conn 的代码
	// （消费者 conn.Channel()）触发 nil pointer panic。此问题曾实际出现过：发布路径
	// 只用 channel（那行 = 写对了）一切正常，消费者首个使用 conn 的调用即
	// panic。Go 经典陷阱，govet 的 shadow 分析器可检出。
	url := fmt.Sprintf("amqp://%s:%s@%s:%d%s", cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Vhost)
	var err error              // 显式声明 err，下面的赋值才能对包级变量生效
	conn, err = amqp.Dial(url) // = 赋值包级 conn（真实连接）
	if err != nil {
		return fmt.Errorf("mq: dial: %w", err)
	}
	// 开 channel：RabbitMQ 的命令通道（连接上的轻量会话，发布/声明都走它）
	channel, err = conn.Channel()
	if err != nil {
		return fmt.Errorf("mq: open channel: %w", err)
	}
	// 声明 direct 交换机（durable：broker 重启后存在）
	if err := channel.ExchangeDeclare(
		cfg.Exchange, "direct",
		true,
		false,
		false,
		false,
		nil); err != nil {
		return fmt.Errorf("mq: declare exchange: %w", err)
	}
	// 声明建单队列（durable）。队列在 Init 统一声明：
	// durable 队列让消息落盘积压（管理界面可见），
	// 消费者上线后从队列头开始消化——消息不因"还没人消费"而丢失。
	if _, err := channel.QueueDeclare(
		GrabOrderQueue,
		true,
		false,
		false,
		false,
		nil); err != nil {
		return fmt.Errorf("mq: declare grab order queue: %w", err)
	}
	// 绑定：队列以 routing key 挂到交换机——不绑定则消息路由无目的地，直接丢弃
	if err := channel.QueueBind(
		GrabOrderQueue,
		GrabOrderRoutingKey,
		cfg.Exchange,
		false,
		nil); err != nil {
		return fmt.Errorf("mq: bind grab order queue: %w", err)
	}
	exchangeName = cfg.Exchange
	return nil
}

// Close 关闭连接（channel 随连接级联关闭）
func Close() {
	if conn != nil {
		_ = conn.Close()
	}
}
