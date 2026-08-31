// Package logic 业务逻辑层：编排 DAO 与工具函数，实现各模块业务规则。
// 本文件为延时任务扫描器（内部 goroutine + 10s ticker，不引入新组件）：
//   - 职责A 订单超时关单：ZSet 到期捞取 → 条件 UPDATE 关单事务 →
//     事务提交后释放 Redis 名额（守恒式记账）→ 移除任务
//   - 职责B 拼单截止判定：互斥条件 UPDATE 批量翻终态（failed/succeeded）
//
// 扩展性：多实例部署时两个扫描器并发执行同一批 UPDATE 也安全——
// 行锁 + 状态守卫天然互斥，后到者 rows=0 落空，无需分布式锁。
package logic

import (
	"fmt"
	"strconv"
	"time"

	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/model"
	"campuscommunity/internal/mq"

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
// 失败策略：单条 DB 故障【不移除任务】，留在 ZSet 等下一轮重试——关单
// 是条件 UPDATE，幂等重试零风险；若失败即移除，一次 DB 抖动就造出一批
// 永不关闭的僵尸订单。单条失败也不中断整轮（不影响其他订单），仅记日志。
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
		// 通知投递（挂尾部 best-effort）：「订单已关闭（超时未支付）」。
		// 扫描器现场上下文薄（只有三个 id，无标题/金额）——对照建单现场
		// 「上下文全则内容丰富」，此处文案从简只报订单号；如需金额可补
		// 一次主键查询（冷路径：10s 一轮、每单一条，微秒级查得起）。
		// 落空分支（!closed）不发通知：支付路径自己已发「已支付」，
		// 两路互斥由状态机守卫保证，不会同时成立。
		notifyBestEffort(mq.NotificationMessage{
			UserID:   userID,
			Type:     string(model.NotifyOrder),
			Category: string(model.CategoryClosed),
			RefID:    orderID,
			Title:    "订单已关闭",
			Content:  fmt.Sprintf("订单 #%d 超过 30 分钟未支付，已自动关闭，参与名额已退回", orderID),
		})
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

// judgeExpiredGroupBuys 职责B：拼单截止判定 + 终态批量通知。
// 幂等性由逐行条件 UPDATE 的状态守卫保证（已翻过的行 rows=0 落空且不
// 进入返回列表），无轮间状态。通知挂翻转之后（与订单类挂载点同一铁律：
// 翻转成功才有资格通知，落空者零通知）。
func judgeExpiredGroupBuys() {
	failedIDs, succeededIDs, err := dao.JudgeExpiredGroupBuys()
	if err != nil {
		zap.L().Error("logic: judge expired group buys failed", zap.Error(err))
		return
	}
	if len(failedIDs) == 0 && len(succeededIDs) == 0 {
		return // 常态空轮：无翻终态零通知，直接出局省一轮日志
	}
	zap.L().Info("logic: deadline judged",
		zap.Int("failed", len(failedIDs)), zap.Int("succeeded", len(succeededIDs)))
	// ---- 通知投递：翻终态的每行 → 拉拼单快照 → 批量通知成员+发布者 ----
	// 逐单独立处理：单条拉快照失败只丢该单的通知（记日志 continue），
	// 不拖垮整批——与其他扫描器循环一致的「单条失败不中断」策略
	for _, id := range failedIDs {
		gb, err := dao.GetGroupBuyByID(id)
		if err != nil || gb == nil {
			// err=DB 故障；gb=nil=行已被物理删除（发布回滚残留）——
			// 两者都无法定位收件人，丢弃该单通知
			zap.L().Error("logic: get group buy for failed notify failed, dropped",
				zap.Int64("good_id", id), zap.Error(err))
			continue
		}
		notifyGroupBuyEvent(gb, model.CategoryFailed, "拼单失败",
			fmt.Sprintf("拼单「%s」已截止，人数未达最低成团人数，拼单失败", gb.Title))
	}
	// succeeded 分支 = 「翻漏补翻」（正常时序建单事务已翻 succeeded 并已通知过）。
	// 幂等妙处：若用户此前已收过成团通知，本条撞 (user, succeeded, good_id)
	// 唯一索引被静默吞掉；只有真正漏掉的（建单翻转成功但通知失败的幸存者）
	// 第一次收到——通知消费者的唯一索引天然就是「补发去重器」。
	for _, id := range succeededIDs {
		gb, err := dao.GetGroupBuyByID(id)
		if err != nil || gb == nil {
			zap.L().Error("logic: get group buy for succeeded notify failed, dropped",
				zap.Int64("good_id", id), zap.Error(err))
			continue
		}
		notifyGroupBuyEvent(gb, model.CategorySucceeded, "拼单已成团",
			fmt.Sprintf("您参与的拼单「%s」已成团，最低人数已凑齐，请尽快完成支付", gb.Title))
	}
}
