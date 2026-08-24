// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为订单超时关闭的 ZSet 延时队列操作（建单成功后投递延时任务，
// 延时扫描器按 score 到期捞取关单）。
package redis

import (
	"fmt"
	"time"

	"github.com/go-redis/redis"
)

// delayOrderCloseKey 延时关单队列键：delay:order:close（全局单键）。
// 设计：member = order_id（雪花），score = 到期时间戳（秒）。
// 全局单键而非按拼单分键：扫描器一次 ZRANGEBYSCORE 捞全平台到期订单，
// 无需遍历拼单维度；ZSet 内部按 score 排序，成员量 = 待关闭订单数（有界
// ——每单只在 pending_pay 期内在队，支付/取消即 ZREM）。
func delayOrderCloseKey() string {
	return "delay:order:close"
}

// orderCloseDelay 订单支付超时时间：30 分钟（订单状态机：
// pending_pay 超时 → closed；以及项目硬约束「订单超时 30 分钟」）。
// 到期后订单关闭、名额释放（INCR stock + 状态翻 closed + 通知），
// 全部动作由延时扫描器执行，本常量只负责「什么时候到期」。
const orderCloseDelay = 30 * time.Minute

// EnqueueOrderClose 建单成功后投递延时关单任务（消费者建单第 5 步）。
// ZADD delay:order:close <now+30min 的秒级时间戳> <order_id>：
//   - score 是秒级 Unix 时间戳（ZSet score 为 float64，秒级精度对 30 分钟
//     超时绰绰有余；扫描间隔 10s 决定了实际关闭精度，纳秒级 score 无意义）
//   - member 是 order_id 字符串化（Redis 一切皆字符串；雪花 int64 需转
//     string 写入，扫描器读出后 strconv.ParseInt 还原——写入读取
//     两端类型口径必须一致，这是 ZSet 成员类型的既有教训）
//
// 重复投递安全性：ZADD 对已存在 member 是「更新 score」语义——同一订单
// 不会产生重复任务行（补偿扫描若重发建单，唯一索引已挡，不会走到
// 本函数；即便走到，ZADD 覆盖也只是重置到期时间，不产生双关单）。
//
// 失败处理：best-effort——延时任务属派生数据（订单表 status 本身就是
// 关单判定的真值源，ZSet 只是「到点提醒」的索引），丢失的兜底是补偿任务
// （全表扫描 pending_pay 超时订单补偿关闭，规划中）。logic 层调用失败
// 仅记 error 日志，不回滚已提交的建单事务（核心事实优先，派生数据靠补偿）。
func EnqueueOrderClose(orderID int64) error {
	// 到期时间 = 当前时间 + 30min；Unix() 取秒级时间戳作 score
	executeAt := time.Now().Add(orderCloseDelay).Unix()
	err := client.ZAdd(delayOrderCloseKey(), redis.Z{
		Score:  float64(executeAt),
		Member: fmt.Sprintf("%d", orderID),
	}).Err()
	if err != nil {
		return fmt.Errorf("redis: enqueue order close: %w", err)
	}
	return nil
}

// DequeueExpiredOrderCloses 取出到期的关单任务（延时扫描器每 10s 调用）。
// ZRANGEBYSCORE key -inf <now>：捞出所有 score（到期时间）已过当前时刻的
// order_id。注意是「读不删」——真正删除由处理方成功关单后 ZREM：
// 读删若不原子，崩溃窗口内任务会丢失（读了还没删进程就挂了）；先读后删
// 配合关单幂等（状态机条件 UPDATE 只允许 pending_pay→closed），即使重读
// 已处理的任务也不会双关单。
// 上限 limit：单轮处理量有界，防止积压台风式灌入一次性拖垮单次扫描
// （每单关单涉及 3 次 DB 写 + Redis 释放，1000 上限是防御值）。
func DequeueExpiredOrderCloses(now time.Time) ([]string, error) {
	ids, err := client.ZRangeByScore(delayOrderCloseKey(), redis.ZRangeBy{
		Min:   "-inf",
		Max:   fmt.Sprintf("(%d", now.Unix()), // "(" 前缀 = 开区间，不含 now 本身
		Count: 1000,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: dequeue expired order closes: %w", err)
	}
	return ids, nil
}

// RemoveOrderClose 任务完成后从延时队列移除（关单成功后调用）。
// 关单失败时【不删】：任务留在 ZSet 里等下一轮扫描重试——关单操作
// 本身幂等（条件 UPDATE 只认 pending_pay），重试安全。
func RemoveOrderClose(orderID int64) error {
	if err := client.ZRem(delayOrderCloseKey(), fmt.Sprintf("%d", orderID)).Err(); err != nil {
		return fmt.Errorf("redis: remove order close: %w", err)
	}
	return nil
}
