// 建单消费者：把 mq 基础设施（consumer.go）与业务编排（logic/order.go）
// 粘合起来的适配层——本文件属于 main 包装配的一部分，但为保持 mq 包
// 对业务零感知，适配函数由 main 注入（依赖倒置，解 mq→logic 与
// logic→mq 的 import cycle）。
package mq

// GrabOrderQueue/RoutingKey 见 rabbitmq.go（拓扑常量，生产与消费共用）。
// 本文件不解析 GrabOrderMessage——那是 main 适配函数的职责。
