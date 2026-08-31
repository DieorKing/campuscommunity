// Package logic 业务逻辑层：编排 DAO 与工具函数，实现各模块业务规则。
// 本文件为对账扫描器：比对 Redis 预扣标记（members 集合）与订单事实
// （orders 表），差集 = 建单消息丢失，重发消息修复。与消息重试
// （nack 重投）、补偿任务（落任务退避重试）互补，各接一类故障。
//
// 数据流：遍历全部拼单（分页）→ SMEMBERS 拉预扣成员 → 逐个查订单
// → 差集（members 有 + orders 无 + 预扣超时）→ 重发建单消息。
//
// 已知边界：全量 SMEMBERS 每集合 O(N)，拼单数上千时单轮开销增大——
// 大规模场景需改为增量扫描（按活跃时间圈定范围）或 DB 索引反转扫描。
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/model"
	"campuscommunity/internal/mq"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// reconcileInterval 对账扫描间隔：60s。对账是兜底层——消息丢失是
// 低频事件，重发有幂等兜底，高频轮扫无意义；60s 意味着最坏 1 分钟
// 后发现并修复，业务可接受。
const reconcileInterval = 60 * time.Second

// reconcilePendingTimeout 差集判定的预扣超时阈值：1 分钟。
// 语义=「消息大概率已不在队列」：刚预扣的消息可能还在队列排队
// （削峰积压），此时重发属误判——1 分钟阈值下重叠窗口极小，
// 且误重发零成本（消费端 uk_user_good 幂等吸收）。
const reconcilePendingTimeout = time.Minute

// StartReconcileScanner 启动对账扫描器（main.go 放独立 goroutine，永久运行）。
// 与延时/补偿扫描器共用 ticker 模式，独立 goroutine 独立节奏——
// 失败域隔离：对账是兜底层，它的改动/故障不应影响正常路径的扫描器。
func StartReconcileScanner() {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	zap.L().Info("logic: reconcile scanner started",
		zap.Duration("interval", reconcileInterval))
	for range ticker.C {
		reconcileGrabOrders()
	}
}

// reconcileGrabOrders 单轮对账：全量差集扫描 + 重发建单消息。
// 策略：单条失败不中断整轮（不影响其他拼单）；Redis/DB 故障本轮放弃
// 下轮再来（对账是幂等比对，重扫零风险）。
func reconcileGrabOrders() {
	// 1. 分页遍历全部拼单（含终态——消息丢失不挑状态，且终态拼单的
	//    预扣残留也要对出来：如成团后关单的遗留名额）
	page, pageSize := 1, 100
	for {
		gbs, total, err := dao.ListGroupBuyPage(page, pageSize)
		if err != nil {
			zap.L().Error("logic: reconcile list group buys failed", zap.Error(err))
			return
		}
		for i := range gbs {
			reconcileGroupBuy(&gbs[i])
		}
		// 遍历完分页退出
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
}

// reconcileGroupBuy 单个拼单的对账：SMEMBERS 预扣成员 → 逐个比订单
// → 差集且超时 → 重发消息。
func reconcileGroupBuy(gb *model.GroupBuy) {
	// 拉预扣成员（string 形态 user_id）
	memberStrs, err := redis.ListGroupBuyMemberIDs(gb.GoodID.Int64())
	if err != nil {
		// Redis 故障：跳过该拼单本轮对账（集合读不到没法比）
		zap.L().Error("logic: reconcile list members failed",
			zap.Int64("good_id", gb.GoodID.Int64()), zap.Error(err))
		return
	}
	for _, m := range memberStrs {
		userID, err := strconv.ParseInt(m, 10, 64)
		if err != nil {
			// 脏数据防御：成员全由 Lua 脚本 %d 写入，理论不可达
			zap.L().Error("logic: reconcile member invalid",
				zap.Int64("good_id", gb.GoodID.Int64()), zap.String("member", m))
			continue
		}
		// 查订单事实：(nil, nil) = 没建单
		order, err := dao.GetOrderByUserAndGood(userID, gb.GoodID.Int64())
		if err != nil {
			// DB 故障：跳过该成员（单条失败不中断）
			zap.L().Error("logic: reconcile get order failed",
				zap.Int64("good_id", gb.GoodID.Int64()), zap.Int64("user_id", userID), zap.Error(err))
			continue
		}
		if order != nil {
			continue // 订单在：正常路径，无需对账
		}
		// 差集命中（members 有 + orders 无）→ 判定预扣时长是否超阈值：
		// members 无时间戳（Set 只有成员），用「该成员订单查询 miss 且
		// 拼单发布超过 1 分钟」近似——发布时间是最接近预扣时间的可查值
		//（预扣只发生在发布后），差集成员的预扣必然晚于发布早于现在。
		// 该近似在「发布即抢」场景（本项目主场景）误差最小。
		if time.Since(gb.CreatedAt) < reconcilePendingTimeout {
			continue // 拼单太新：消息可能还在队列排队，别误重发
		}
		// 重发建单消息：已建单的撞 uk_user_good 幂等吸收；真丢的补上。
		// 与 grab.go step4 的 Publish 同一语义（预扣事实驱动建单）
		if err := mq.PublishGrabOrder(gb.GoodID.Int64(), userID); err != nil {
			// 重发失败：下轮对账再试（差集还在，天然重试）
			zap.L().Error("logic: reconcile republish failed",
				zap.Int64("good_id", gb.GoodID.Int64()), zap.Int64("user_id", userID), zap.Error(err))
			continue
		}
		zap.L().Info("logic: reconcile republished grab order (message lost recovered)",
			zap.Int64("good_id", gb.GoodID.Int64()), zap.Int64("user_id", userID))
	}
}
