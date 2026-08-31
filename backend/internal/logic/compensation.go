// Package logic 业务逻辑层：编排 DAO 与工具函数，实现各模块业务规则。
// 本文件为补偿模块的生产侧统一入口：各 best-effort 失败分支调用
// compensate 落任务（DB 行 + ZSet 调度），把「失败只躺在日志里」升级为
// 「失败在 DB 里排队等重试」。
// 三个失败点挂载：
//   - order.go CreateOrderByMessage 5a：热榜 ZINCRBY 失败（重算型）
//   - order.go CreateOrderByMessage 5b：延时关单 ZADD 失败（重放型）
//   - notification.go notifyBestEffort：通知投递失败（重放型）
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/model"
	"campuscommunity/internal/mq"
	"campuscommunity/pkg/utils/snowflake"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// compensate 落补偿任务（失败现场的统一入口）。
// 编排：INSERT 任务行(pending) → ZADD 调度(score=now，首次重试交给
// 扫描器下一轮，10s 粒度天然就是第一次退避)。
//
// 失败降级链（补偿动作自身失败无法再被补偿，最外层以日志+人工兜底）：
//  1. 落任务 INSERT 失败 → 纯日志（回到 best-effort 原行为）
//  2. ZADD 调度失败 → 日志，但 DB 行已在（next_retry_at 可见），
//     兜底是人工/全表对账捞 pending 超时行——记日志保留排查线索
//
// 注意与 notifyBestEffort 的层级关系：通知失败 → 先落补偿任务；
// 落补偿任务也失败 → 才降级纯日志。补偿是 best-effort 的加强版兜底，
// 不是替代。
func compensate(taskType model.CompensationType, payload string) {
	now := time.Now()
	t := &model.CompensationTask{
		TaskID:      model.ID(snowflake.GenID()),
		Type:        taskType,
		Payload:     payload,
		Status:      model.CompPending,
		NextRetryAt: now, // 首次重试=now：扫描器 10s 粒度即第一次退避
	}
	// 步1：DB 落行（真值源：行在=欠着）
	if err := dao.CreateCompensationTask(t); err != nil {
		zap.L().Error("logic: create compensation task failed, degraded to log-only",
			zap.String("type", string(taskType)), zap.String("payload", payload), zap.Error(err))
		return
	}
	// 步2：ZSet 入调度（score=now，下一轮扫描即执行）
	if err := redis.EnqueueCompRetry(t.TaskID.Int64(), now); err != nil {
		// DB 行已在但没入调度：行不会丢（人工/对账可捞 pending 超时行），
		// 明确记日志留排查线索——不静默吞
		zap.L().Error("logic: enqueue compensation retry failed, row kept (needs reconciliation)",
			zap.Int64("task_id", t.TaskID.Int64()), zap.Error(err))
	}
}

// ---- 各类型的 payload 结构与落任务助手 ----
// payload 结构私有于本包（表结构不感知，消费端按 type 反序列化同构体）。

// compHotRankPayload 热榜重算型 payload：只带 good_id——计数可从订单表
// 重算（COUNT），不塞结果值（塞了就是过期快照，补偿语义要求以执行
// 时刻的 DB 为准）。
type compHotRankPayload struct {
	GoodID int64 `json:"good_id"`
}

// compOrderClosePayload 延时关单重放型 payload：只带 order_id——
// ZADD 的 score（到期时间）由消费者执行时重算 now+30min，重试一次
// 就是新窗口，无累积伤害。
type compOrderClosePayload struct {
	OrderID int64 `json:"order_id"`
}

// CompensateHotRank 热榜失败落任务（重算型：消费者 COUNT+ZADD 覆盖）。
// 调用点：CreateOrderByMessage 5a 分支。
func CompensateHotRank(goodID int64) {
	payload := compHotRankPayload{GoodID: goodID}
	// 序列化失败不可能（结构体只含 int64 字段），防御性处理仍走日志降级
	body, err := json.Marshal(payload)
	if err != nil {
		zap.L().Error("logic: marshal hot rank compensation payload failed",
			zap.Int64("good_id", goodID), zap.Error(err))
		return
	}
	compensate(model.CompHotRank, string(body))
}

// CompensateOrderClose 延时关单失败落任务（重放型：消费者原样 ZADD）。
// 调用点：CreateOrderByMessage 5b 分支。
func CompensateOrderClose(orderID int64) {
	payload := compOrderClosePayload{OrderID: orderID}
	body, err := json.Marshal(payload)
	if err != nil {
		zap.L().Error("logic: marshal order close compensation payload failed",
			zap.Int64("order_id", orderID), zap.Error(err))
		return
	}
	compensate(model.CompOrderClose, string(body))
}

// CompensateNotification 通知投递失败落任务（重放型：payload=完整消息）。
// 文案不可重算（人写的不是查出来的），重放必须带全套内容——失败现场
// 的 msg 整体 JSON 化入列，消费者直接 Unmarshal 重发。
// 调用点：notifyBestEffort 的 PublishNotification 失败分支。
func CompensateNotification(msg mq.NotificationMessage) {
	body, err := json.Marshal(msg)
	if err != nil {
		zap.L().Error("logic: marshal notification compensation payload failed",
			zap.Int64("user_id", msg.UserID), zap.Error(err))
		return
	}
	compensate(model.CompNotification, string(body))
}

// ── 消费侧：补偿扫描器（沿用延时扫描器的 ticker 模式） ──────────

// StartCompensationScanner 启动补偿扫描器（main.go 放独立 goroutine，
// 永久运行）。每轮：ZSet 捞到期 → 差集清理 ZREM → 回表 pending 行 →
// 逐任务执行补偿动作 → 按结果删行/退避/翻终态。
// 幂等与并发安全：多实例同时扫也安全——任务执行幂等（重放型天然、
// 重算型绝对值），FailCompensationTask 的 WHERE status=pending 守卫
// 防计数双加，先成功者删行后到者捞空。
func StartCompensationScanner() {
	ticker := time.NewTicker(delayScanInterval) // 沿用延时扫描的 10s 粒度
	defer ticker.Stop()
	zap.L().Info("logic: compensation scanner started",
		zap.Duration("interval", delayScanInterval))
	for range ticker.C {
		processDueCompensations()
	}
}

// processDueCompensations 单轮补偿处理。
func processDueCompensations() {
	// 1. ZSet 捞到期 task_id（读不删——崩溃安全：读了没处理完，
	//    任务还在，下轮重捞）
	idStrs, err := redis.DequeueDueCompRetries(time.Now())
	if err != nil {
		// Redis 故障：本轮放弃下轮再来（与 closeExpiredOrders 同策略）
		zap.L().Error("logic: comp dequeue failed", zap.Error(err))
		return
	}
	if len(idStrs) == 0 {
		return // 常态空轮
	}
	// 2. 解析 id（member 全由 EnqueueCompRetry %d 写入，理论不可达，
	//    防御性跳过策略与关单扫描一致）
	ids := make([]int64, 0, len(idStrs))
	for _, s := range idStrs {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			zap.L().Error("logic: comp task member invalid, skipping",
				zap.String("member", s), zap.Error(err))
			continue
		}
		ids = append(ids, id)
	}
	// 3. 回表取 pending 行（过滤已删/已终态——ZSet 残留的正常容忍）
	tasks, err := dao.GetDueCompensationTasks(ids)
	if err != nil {
		// DB 故障：任务仍在 ZSet（读不删），下轮重捞——零丢失
		zap.L().Error("logic: comp get due tasks failed, will rescan", zap.Error(err))
		return
	}
	// 4. 差集清理（ZSet 残留泄漏防御）：捞到但 DB 无 pending 行的 id =
	//    ZREM 失败的残留 / 已删行。不清理则永远每轮空捞一次。
	//    差集 = 捞到的 ids - 返回的 task_ids
	present := make(map[int64]struct{}, len(tasks))
	for i := range tasks {
		present[tasks[i].TaskID.Int64()] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := present[id]; !ok {
			// 残留清理 best-effort：再失败下轮再来（窗口最多一轮）
			if err := redis.RemoveCompRetry(id); err != nil {
				zap.L().Error("logic: comp residual cleanup failed",
					zap.Int64("task_id", id), zap.Error(err))
			}
		}
	}
	// 5. 逐任务执行：单条失败不中断整轮（不影响其他任务），与关单扫描同策略
	for i := range tasks {
		runCompensationTask(&tasks[i])
	}
}

// runCompensationTask 执行单个补偿任务：type 分流 → 动作 → 结果流转。
// 动作全部幂等（落任务前已审幂等性），失败只剩暂时性故障——退避重试；
// 5 次封顶翻 failed 终态留人工（重试无法修复的出口）。
func runCompensationTask(t *model.CompensationTask) {
	var err error
	switch t.Type {
	case model.CompHotRank:
		err = execHotRankCompensation(t.Payload)
	case model.CompOrderClose:
		err = execOrderCloseCompensation(t.Payload)
	case model.CompNotification:
		err = execNotificationCompensation(t.Payload)
	default:
		// 未知类型：落任务侧新增类型但消费侧未升级（部署版本错位）——
		// 确定性失败，直接翻终态不重试（重试一万次还是不认识）
		zap.L().Error("logic: unknown compensation type, terminal",
			zap.Int64("task_id", t.TaskID.Int64()), zap.String("type", string(t.Type)))
		terminalFailQuietly(t, "unknown compensation type")
		return
	}

	if err == nil {
		// 成功：删行 + ZREM 调度项（best-effort，残留
		// 由差集清理兜底）
		if err := dao.SucceedCompensationTask(t.TaskID.Int64()); err != nil {
			zap.L().Error("logic: comp succeed delete failed, will rescan",
				zap.Int64("task_id", t.TaskID.Int64()), zap.Error(err))
			return // 删行失败：任务留在原地，下轮重捞重执行（幂等安全）
		}
		removeCompRetryQuietly(t.TaskID.Int64())
		zap.L().Info("logic: compensation task succeeded",
			zap.Int64("task_id", t.TaskID.Int64()), zap.String("type", string(t.Type)),
			zap.Int("retries", t.RetryCount))
		return
	}

	// 失败：退避重入队 / 翻终态（FailCompensationTask 内部按 retry 计数分流）
	retryCount, terminal, ferr := dao.FailCompensationTask(t.TaskID.Int64(), err.Error())
	if ferr != nil {
		// DB 更新失败：行仍 pending、ZSet 项仍在（读不删模式），下轮
		// 重捞重执行——幂等保证无副作用累积，最多多试几轮
		zap.L().Error("logic: comp fail update failed, will rescan",
			zap.Int64("task_id", t.TaskID.Int64()), zap.Error(ferr))
		return
	}
	if terminal {
		// 翻终态：清调度项（不清则差集清理每轮捞它一次）
		removeCompRetryQuietly(t.TaskID.Int64())
		zap.L().Error("logic: compensation task terminal failed (manual attention)",
			zap.Int64("task_id", t.TaskID.Int64()), zap.String("type", string(t.Type)),
			zap.Int("retry_count", retryCount), zap.Error(err))
		return
	}
	// 退避重入队：score = now + BackoffDelay(retryCount)
	//（FailCompensationTask 已把 next_retry_at 同步写行，这里 ZSet 对齐）
	if err := redis.EnqueueCompRetry(t.TaskID.Int64(),
		time.Now().Add(model.BackoffDelay(retryCount))); err != nil {
		// ZADD 失败：行仍在且 next_retry_at 已推进——对账兜底（同落任务侧）
		zap.L().Error("logic: comp re-enqueue failed, row kept (needs reconciliation)",
			zap.Int64("task_id", t.TaskID.Int64()), zap.Error(err))
	}
	zap.L().Warn("logic: compensation task failed, backing off",
		zap.Int64("task_id", t.TaskID.Int64()), zap.Int("retry_count", retryCount),
		zap.Error(err))
}

// terminalFailQuietly 未知类型的确定性终态：直接翻 failed 不走退避
// （重试无意义——版本错位不会因重试而修复）。
func terminalFailQuietly(t *model.CompensationTask, reason string) {
	_, _, err := dao.FailCompensationTask(t.TaskID.Int64(), reason)
	if err != nil {
		zap.L().Error("logic: comp terminal update failed", zap.Error(err))
	}
	// 强制终态：FailCompensationTask 按计数分流，未知类型不重试
	// 直接转终态——即使计数未达上限也清调度（防无限空捞）
	removeCompRetryQuietly(t.TaskID.Int64())
}

// removeCompRetryQuietly 清调度项（best-effort，差集清理兜底）。
func removeCompRetryQuietly(taskID int64) {
	if err := redis.RemoveCompRetry(taskID); err != nil {
		zap.L().Error("logic: remove comp retry failed (diff cleanup will handle)",
			zap.Int64("task_id", taskID), zap.Error(err))
	}
}

// ---- 三个补偿动作执行器 ----

// execHotRankCompensation 热榜重算：COUNT 有效订单 → ZADD 绝对值覆盖。
// 不用 ZINCRBY：相对操作在「响应超时但服务端已执行」场景下重放会
// 造成双重计数，绝对值覆盖则天然幂等。
func execHotRankCompensation(payload string) error {
	var p compHotRankPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		// payload 是自己落的（格式契约在包内），畸形=代码 bug——
		// 包装上抛走退避重试（重试也不解，最终翻终态暴露给人工）
		return fmt.Errorf("hot rank payload malformed: %w", err)
	}
	// 以执行时刻 DB 为准重算（快照会过期，重算不会）
	count, err := dao.CountOrdersByGoodID(p.GoodID)
	if err != nil {
		return fmt.Errorf("compensate hot rank count: %w", err)
	}
	if err := redis.ZAddHotRank(p.GoodID, float64(count)); err != nil {
		return fmt.Errorf("compensate hot rank zadd: %w", err)
	}
	return nil
}

// execOrderCloseCompensation 延时关单重放：原样 ZADD，score 重算新窗口。
func execOrderCloseCompensation(payload string) error {
	var p compOrderClosePayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("order close payload malformed: %w", err)
	}
	// 原命令重放：入队语义与建单 5b 完全一致（ZADD 覆盖幂等）
	if err := redis.EnqueueOrderClose(p.OrderID); err != nil {
		return fmt.Errorf("compensate order close: %w", err)
	}
	return nil
}

// execNotificationCompensation 通知重发：payload 即完整消息，直接重投。
// 重复投递由消费端 (user, category, ref_id) 唯一索引静默吸收。
func execNotificationCompensation(payload string) error {
	var msg mq.NotificationMessage
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		return fmt.Errorf("notification payload malformed: %w", err)
	}
	if err := mq.PublishNotification(msg); err != nil {
		return fmt.Errorf("compensate notification publish: %w", err)
	}
	return nil
}
