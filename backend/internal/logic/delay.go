// Package logic 业务逻辑层：编排 DAO 与工具函数，实现各模块业务规则。
// 本文件为延时任务扫描器（内部 goroutine + 10s ticker，不引入新组件）：
//   - 职责A 订单超时关单：ZSet 到期捞取 → 条件 UPDATE 关单事务 →
//     事务提交后释放 Redis 名额（守恒式记账）→ 移除任务
//   - 职责B 拼单截止判定：互斥条件 UPDATE 批量翻终态（failed/succeeded）
// 扩展性：多实例部署时两个扫描器并发执行同一批 UPDATE 也安全——
// 行锁 + 状态守卫天然互斥，后到者 rows=0 落空，无需分布式锁。
package logic

import (
	"strconv"
	"time"

	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"

	"go.uber.org/zap"
)

// delayScanInterval 扫描间隔 10s：决定关单/判定的实际精度（30min 超时
// 实际在 30~30.17min 间关闭，业务无感）。间隔权衡：越短越及时但空转
// 越多（ZRANGEBYSCORE + 2 条 UPDATE 皆 O(到期数)，空轮成本可忽略，
// 10s 业务常用）。
const delayScanInterval = 10 * time.Second

// StartDelayScanner 启动延时扫描器（main.go 放独立 goroutine，永久运行，
// 随进程退出而终止——任务不丢：未 ZREM 的任务留在 ZSet，重启后下轮补扫）。
func StartDelayScanner() {
	ticker := time.NewTicker(delayScanInterval)
	defer ticker.Stop()
	zap.L().Info("logic: delay scanner started", zap.Duration("interval", delayScanInterval))
	for range ticker.C {
		closeExpiredOrders()
		judgeExpiredGroupBuys()
	}
}

// closeExpiredOrders 职责A：捞出到期关单任务并逐条处理。
// 失败策略（对既有设计文档「异常即 ZREM」的有意修正）：
// 单条 DB 故障【不移除任务】，留在 ZSet 等下一轮重试——关单是条件
// UPDATE，幂等重试零风险；若失败即移除，一次 DB 抖动就造出一批
// 永不关闭的僵尸订单，只能等全表补偿扫描救回。单条失败也不中断
// 整轮（其他订单无辜），仅记日志。
func closeExpiredOrders() {
	ids, err := redis.DequeueExpiredOrderCloses(time.Now())
	if err != nil {
		// Redis 故障：本轮放弃，下轮再来（不致命）
		zap.L().Error("logic: delay close dequeue failed", zap.Error(err))
		return
	}
	for _, idStr := range ids {
		orderID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			// member 全部由 EnqueueOrderClose 写入（%d 格式化），理论上
			// 不可达；防御性记日志不处理（无有效 orderID 可删）
			zap.L().Error("logic: delay close task member invalid",
				zap.String("member", idStr), zap.Error(err))
			continue
		}

		goodID, userID, closed, err := dao.CloseExpiredOrder(orderID)
		if err != nil {
			// DB 故障（暂时性）：保留任务，下一轮重试（见函数头注释）
			zap.L().Error("logic: close expired order failed, keep task for retry",
				zap.Int64("order_id", orderID), zap.Error(err))
			continue
		}
		if !closed {
			// 状态机落空（已支付/已取消/订单不存在）：任务使命已了，移除。
			// 典型场景：用户在 29:59 完成支付赢得竞态——「订单已关闭」通知
			// 的反面：这里什么都不用补，支付路径自己已收尾
			zap.L().Info("logic: delay close skipped, order not in pending_pay",
				zap.Int64("order_id", orderID))
			removeCloseTaskQuietly(orderID)
			continue
		}

		// 关单成功 → 事务已提交，释放 Redis 名额（INCR stock + SREM members）。
		// 守恒式记账：每次释放对称回补，不按拼单状态分支（recruiting 时
		// 名额回流可再抢；成团后入口拦截，回补只为账目守恒可对账）。
		// best-effort：失败是少卖方向，记日志靠补偿
		if err := redis.ReleaseGroupBuySlot(goodID, userID); err != nil {
			zap.L().Error("logic: release slot after close failed (under-sell direction, tolerable)",
				zap.Int64("good_id", goodID), zap.Int64("user_id", userID), zap.Error(err))
		}
		removeCloseTaskQuietly(orderID)
		zap.L().Info("logic: order closed by delay scanner",
			zap.Int64("order_id", orderID), zap.Int64("good_id", goodID),
			zap.Int64("user_id", userID))
	}
}

// removeCloseTaskQuietly 移除关单任务（best-effort）。失败无害自愈：
// 任务残留 → 下一轮重捞 → 关单 rows=0 落空 → 再走移除——最多多扫一轮。
func removeCloseTaskQuietly(orderID int64) {
	if err := redis.RemoveOrderClose(orderID); err != nil {
		zap.L().Error("logic: remove close task failed (will rescan, idempotent)",
			zap.Int64("order_id", orderID), zap.Error(err))
	}
}

// judgeExpiredGroupBuys 职责B：拼单截止判定。
// 幂等性由条件 UPDATE 的状态守卫保证（已翻过的行 rows=0），无轮间状态。
// 通知（成团/失败）待通知模块接入后在此挂载。
func judgeExpiredGroupBuys() {
	failed, succeeded, err := dao.JudgeExpiredGroupBuys()
	if err != nil {
		zap.L().Error("logic: judge expired group buys failed", zap.Error(err))
		return
	}
	if failed > 0 || succeeded > 0 {
		zap.L().Info("logic: deadline judged",
			zap.Int64("failed", failed), zap.Int64("succeeded", succeeded))
	}
}
