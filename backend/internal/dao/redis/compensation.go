// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为补偿系统的 ZSet 重试队列操作（失败现场入队，补偿扫描器
// 按 score=下次重试时间到期捞取执行）。
// 设计沿用 delay.go 的「DB 真值源 + ZSet 调度」：任务行在 MySQL
// （compensation_tasks），Redis 只管「什么时候该试了」的精确唤醒——
// ZRANGEBYSCORE O(logN) 到期捞取，优于 DB 轮询 WHERE next_retry_at<NOW()
// （无索引支撑的全表条件扫描）。
package redis

import (
	"fmt"
	"time"

	"github.com/go-redis/redis"
)

// compRetryKey 补偿重试队列键：delay:compensation:retry（全局单键）。
// member = task_id（雪花，字符串化写入——ZSet 成员类型口径与
// delay:order:close 一致），score = 下次重试时间的秒级时间戳。
func compRetryKey() string {
	return "delay:compensation:retry"
}

// EnqueueCompRetry 任务入重试队列（失败现场调用：落任务后立即入队，
// score=now+退避 或 now=首次调度立即试）。
// ZADD 覆盖语义天然支持「重新入队」：重试失败后再入，score 更新为
// 更晚时间——同一 task_id 永远只有一条队列项，无重复。
func EnqueueCompRetry(taskID int64, executeAt time.Time) error {
	err := client.ZAdd(compRetryKey(), redis.Z{
		Score:  float64(executeAt.Unix()),
		Member: fmt.Sprintf("%d", taskID),
	}).Err()
	if err != nil {
		return fmt.Errorf("redis: enqueue compensation retry: %w", err)
	}
	return nil
}

// DequeueDueCompRetries 捞出到期的补偿任务 id（补偿扫描器每轮调用）。
// ZRANGEBYSCORE key -inf <now：「读不删」——删除由处理方按结果执行
// （成功 ZREM / 失败重入队覆盖 score），与关单队列同一崩溃安全设计：
// 读了没处理完进程挂了，任务还在 ZSet 里，重启后下轮重捞。
// Count 上限：单轮处理量有界，防积压集中涌入拖垮单次扫描
// （与 DequeueExpiredOrderCloses 同参数）。
func DequeueDueCompRetries(now time.Time) ([]string, error) {
	ids, err := client.ZRangeByScore(compRetryKey(), redis.ZRangeBy{
		Min:   "-inf",
		Max:   fmt.Sprintf("(%d", now.Unix()),
		Count: 1000,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: dequeue due compensation retries: %w", err)
	}
	return ids, err
}

// RemoveCompRetry 任务出队（补偿成功删行后 / 翻终态后调用）。
// best-effort：ZREM 失败的残留项下一轮会被重捞，但捞到的行已删/已终态
// （GetDueCompensationTasks 过滤 pending），执行之前清理残留项，
func RemoveCompRetry(taskID int64) error {
	if err := client.ZRem(compRetryKey(), fmt.Sprintf("%d", taskID)).Err(); err != nil {
		return fmt.Errorf("redis: remove compensation retry: %w", err)
	}
	return nil
}
