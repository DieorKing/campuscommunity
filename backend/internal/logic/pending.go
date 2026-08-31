// Package logic 业务逻辑层：编排 DAO 与工具函数，实现各模块业务规则。
// 本文件为 pending→success 两阶段状态标记的对账扫描器（失败处理
// 三层防线的第三层）：捞 pending:grab 滞留超时的标记 → 重发建单消息
// → 续期标记（给消费者完整窗口）。
//
// 与重试（nack 重投）、补偿（落任务退避）的分工：
//
//	重试接「报了错的消费失败」，补偿接「报了错的旁路失败」，
//	本扫描器接「没人报错的消息丢失」——pending 标记滞留超时即丢失信号。
//
// 与全量差集对账（上一版方案，已替换）的成本对比：
//
//	全量差集：周期性 SMEMBERS×N + 每成员一次 DB 查询，O(全平台拼单数)
//	pending 标记：热路径 Lua 内 +1 个 O(1) ZADD，消费端 +1 个 ZREM，
//	发现侧一次 ZRANGEBYSCORE O(logN+M)（M=滞留数，常态 0）——
//	用每次 O(1) 的事务内记账换掉周期性 O(N) 的全局对账，
//	高并发下成本结构更优（本地消息表思想）。
//
// 数据流：Lua 预扣成功原子落标记 → MQ 投递 → 消费者建单成功 ZREM
// （确定性失败同样 ZREM——消息已被消费，不会再产出订单）→
// 滞留超 30s = 消息丢失（发布失败/中途丢）→ 重发（幂等兜底）→ 续期。
package logic

import (
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/mq"
	"time"

	"go.uber.org/zap"
)

// pendingScanInterval 对账扫描间隔：10s（与延时/补偿扫描器同一节奏）。
// pending 捞取是 O(logN+M) 的轻查询，常态 M=0 空轮成本可忽略——
// 不需要像上一版全量差集那样降频到 60s。
const pendingScanInterval = 10 * time.Second

// pendingTimeout 标记滞留超时阈值：30s。
// 推导（阈值天平）：误重发代价 ≈ 0（uk_user_good 幂等吸收 + 续期防重复），
// 迟发现代价 = 恢复延迟——代价不对称，偏短配置。
// 下界约束：正常链路 发布→消费→建单 毫秒级；削峰积压场景 QoS=1
// 逐条消化，1 万条积压约 10s 级——30s 覆盖典型积压深度不误发。
// 上界对照：订单 30min 超时关单，30s 的发现窗口比它小两个数量级。
// 实际语义：最坏 30+10s 内发现并重发消息。
const pendingTimeout = 30 * time.Second

// StartPendingScanner 启动 pending 对账扫描器（main.go 放独立
// goroutine，永久运行）。与延时/补偿扫描器共用 ticker 模式，
// 独立 goroutine 独立节奏——失败域隔离：对账是兜底层，它的改动/
// 故障不应影响正常路径的扫描器。
func StartPendingScanner() {
	ticker := time.NewTicker(pendingScanInterval)
	defer ticker.Stop()
	zap.L().Info("logic: pending scanner started",
		zap.Duration("interval", pendingScanInterval),
		zap.Duration("timeout", pendingTimeout))
	for range ticker.C {
		reconcilePendingGrabs()
	}
}

// reconcilePendingGrabs 单轮对账：捞滞留标记 → 重发 → 续期。
// 策略：单条失败不中断整轮（不影响其他标记）；Redis/MQ 故障本轮
// 放弃下轮再来（读不删模式，标记零丢失）。
func reconcilePendingGrabs() {
	members, err := redis.ListExpiredPendingGrabs(time.Now(), pendingTimeout)
	if err != nil {
		// Redis 故障：本轮放弃（标记还在，下轮重捞）
		zap.L().Error("logic: pending list expired failed", zap.Error(err))
		return
	}
	for _, m := range members {
		goodID, userID, err := redis.ParsePendingMember(m)
		if err != nil {
			// 成员全由 pendingMember %d:%d 写入，理论不可达；
			// 脏数据防御：解析失败即无法重发也无法续期，记 error 日志
			// 留人工处理（每轮会重复捞到并重复记日志，可定位）
			zap.L().Error("logic: pending member invalid, manual attention",
				zap.String("member", m), zap.Error(err))
			continue
		}
		republishPendingGrab(goodID, userID)
	}
}

// republishPendingGrab 重发单条建单消息并续期标记。
func republishPendingGrab(goodID, userID int64) {
	// 重发：与 grab.go 投递同一语义（预扣事实驱动建单）。
	// 已建单的撞 uk_user_good 幂等吸收（重复消息 ack 丢弃）；
	// 真丢的补上——重发永远安全。
	if err := mq.PublishGrabOrder(goodID, userID); err != nil {
		// 重发失败：不续期（标记保持超时态），下一轮再试——
		// 天然重试，无需额外机制
		zap.L().Error("logic: pending republish failed, will retry next round",
			zap.Int64("good_id", goodID), zap.Int64("user_id", userID), zap.Error(err))
		return
	}
	// 重发成功：续期标记（score=now），给消费者一个完整的 30s 窗口
	// 消化这条消息。不续期则 10s 后下一轮又判超时重复重发（幂等无害
	// 但浪费）；续期失败仅记日志——标记残留超时下轮会再重发一轮，
	// 仍然幂等安全
	if err := redis.RenewGrabPending(goodID, userID, time.Now()); err != nil {
		zap.L().Error("logic: pending renew failed (harmless, may resend once more)",
			zap.Int64("good_id", goodID), zap.Int64("user_id", userID), zap.Error(err))
	}
	zap.L().Info("logic: pending grab republished (message lost recovered)",
		zap.Int64("good_id", goodID), zap.Int64("user_id", userID))
}
