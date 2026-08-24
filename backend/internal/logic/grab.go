// Package logic 业务逻辑层：编排 DAO 与工具函数，实现抢单模块业务规则。
// 分层约束：不碰 HTTP（不 import gin），不写 SQL/Redis 命令，业务错误用哨兵 error 上抛给 controller 翻译。
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/model"
	"campuscommunity/internal/mq"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// 抢单模块哨兵错误：controller 用 errors.Is 识别并映射为业务响应码。
// ErrGrabBusy 与 ErrGrabSoldOut 的区分是业务语义关键：
// 售罄=库存没了，用户该死心；繁忙=锁被竞争，用户该重试（还有机会成功）。
// 若把锁竞争报成售罄，等于把还能成交的请求劝退（少卖）。
var (
	ErrGrabSoldOut   = errors.New("已售罄")          // Lua 返回 SOLD_OUT 或状态为 full
	ErrGrabDuplicate = errors.New("已参与过该拼单")      // Lua 返回 DUPLICATE（幂等防线一）
	ErrGrabPublisher = errors.New("发布者不能参与自己的拼单") // 业务规则：发布者不可抢自己的单
	ErrGrabBusy      = errors.New("抢单繁忙，请稍后重试")   // 锁竞争失败（≠售罄）
)

// GrabGroupBuy 抢单（POST /api/v1/group-buy/:id/grab，生产者侧六步）。
// userID 来自强制 JWT，goodID 来自路径参数。
//
// 六步编排（每步的失败策略各不相同——分层失败策略的集中体现）：
//  1. 业务校验（状态/截止/非发布者） —— 失败即返回业务哨兵错误
//  2. 分布式锁 SET NX EX             —— 失败返回 ErrGrabBusy（快速失败，1 条命令出局）
//  3. Lua 原子预扣（查重→扣减→登记）  —— 三种返回各自翻译
//  4. MQ 投递建单消息                 —— 失败仅记日志，照常返回受理（预扣永不回滚）
//  5. Lua 释放锁                      —— 失败仅记日志（锁有 TTL 兜底自动过期）
//  6. 返回受理中 grabbed=true, order_id=0
func GrabGroupBuy(userID, goodID int64) (*model.GrabResult, error) {
	// ---------- step 1：业务前置校验（不打 Redis，先过滤明显无效的请求） ----------
	gb, err := dao.GetGroupBuyByID(goodID)
	if err != nil {
		return nil, fmt.Errorf("logic: grab get group buy: %w", err)
	}
	if gb == nil {
		return nil, ErrGroupBuyNotExist
	}
	// 状态校验：full = 名额满（语义同售罄）；其余非 recruiting 均为终态/不可抢
	if gb.Status == model.GroupBuyFull {
		return nil, ErrGrabSoldOut
	}
	if gb.Status != model.GroupBuyRecruiting {
		return nil, ErrGroupBuyExpired
	}
	// 截止时间校验：DB 状态由延时扫描器在截止时翻转为 expired，但扫描有 10s 粒度——
	// 状态还停在 recruiting 而 deadline 已过的窗口内，必须由时间校验兜底拦截
	if time.Now().After(gb.Deadline) {
		return nil, ErrGroupBuyExpired
	}
	// 发布者校验：业务红线——发布者不能参与自己的拼单
	if gb.PublisherID == userID {
		return nil, ErrGrabPublisher
	}

	// ---------- step 2：分布式锁（拼单级互斥 + 快速失败） ----------
	token, ok, err := redis.AcquireLock(goodID)
	if err != nil {
		return nil, fmt.Errorf("logic: grab acquire lock: %w", err)
	}
	if !ok {
		// 锁被他人持有：正常竞争结果，返回「请重试」而非「售罄」——
		// 语义区分见 ErrGrabBusy 注释
		return nil, ErrGrabBusy
	}
	// step 5 的释放挂在 defer：无论后续成功/失败/panic，锁都会被尝试释放
	// （防遗漏；锁本身还有 3s TTL 兜底，双保险）
	defer func() {
		// 释放失败仅记日志：锁已过期或易主时 Lua 比对不通过返回 false，非错误
		if released, rErr := redis.ReleaseLock(goodID, token); rErr != nil || !released {
			zap.L().Warn("logic: release grab lock failed or lock not owned",
				zap.Int64("good_id", goodID), zap.Bool("released", released), zap.Error(rErr))
		}
	}()

	// ---------- step 3：Lua 原子预扣（查重→扣减→登记，防超卖主防线） ----------
	result, err := redis.ExecGrabLua(goodID, userID)
	if err != nil {
		return nil, fmt.Errorf("logic: grab exec lua: %w", err)
	}
	switch result {
	case redis.GrabLuaDuplicate:
		// 幂等防线一（Redis 去重集合）：重复请求在此终结，库存未动
		return nil, ErrGrabDuplicate
	case redis.GrabLuaSoldOut:
		// 脚本内部已 INCR 回滚，stock 恢复原值
		return nil, ErrGrabSoldOut
	case redis.GrabLuaOK:
		// 预扣成功，继续投递
	default:
		// 协议之外的返回值：防御性报错（脚本与常量定义应始终一致）
		return nil, fmt.Errorf("logic: grab lua unexpected result: %s", result)
	}

	// ---------- step 4：MQ 投递建单消息（异步建单的生产者侧终点） ----------
	// 分层失败策略的核心落点：投递失败【不回滚预扣、不返回错误】。
	// 原因（网络二义性）：无法区分「消息没出去 / 确认丢失 / 真丢了」——
	// 若回滚库存而消息其实已到达，消费者建单后名额被他人再抢 = 一个名额两份订单（超卖）。
	// 宁可少卖（可对账修复），不可超卖（不可逆）。丢失的兜底：producer confirm 加固 +
	// error 日志人工对账（规划中）。
	if err := mq.PublishGrabOrder(goodID, userID); err != nil {
		zap.L().Error("logic: publish grab order failed, pre-deduct kept (no rollback)",
			zap.Int64("good_id", goodID), zap.Int64("user_id", userID), zap.Error(err))
	}

	// ---------- step 6：返回受理中 ----------
	// OrderID=0：订单由 MQ 消费者异步创建，前端凭 grabbed=true 转入轮询 /status
	return &model.GrabResult{Grabbed: true, OrderID: 0}, nil
}

// GetGrabStatus 抢单状态轮询（GET /api/v1/group-buy/:id/status，前端 5s 轮询）。
// 异步建单的读侧：用户抢到后凭 (user_id, good_id) 反查订单是否已生成。
//
// 判定顺序（三态，先 Redis 后 MySQL——便宜的在前面）：
//  1. Redis members 无此用户           → 从未抢到，grabbed=false（轮询终点）
//  2. members 有 + DB 无订单           → 受理中（消费者尚未建单），继续轮询
//  3. members 有 + DB 有订单           → 建单完成，返回 order_id（轮询终点）
//
// 注意不查拼单状态：轮询的语义是「我的订单出了没」，与拼单本身状态无关；
// 极端场景（预扣成功后拼单翻终态）由延时关单流程处理，不在本接口职责内。
func GetGrabStatus(userID, goodID int64) (*model.StatusResult, error) {
	// 1. Redis 判参与：SISMEMBER O(1)，未参与直接返回（大多数轮询请求止步于此，
	//    轮询风暴下 MySQL 零压力）
	grabbed, err := redis.IsGroupBuyMember(goodID, userID)
	if err != nil {
		return nil, fmt.Errorf("logic: grab status check member: %w", err)
	}
	if !grabbed {
		return &model.StatusResult{Grabbed: false, OrderID: 0}, nil
	}
	// 2. 已参与 → 查订单是否已生成（消费端幂等建单的结果）
	order, err := dao.GetOrderByUserAndGood(userID, goodID)
	if err != nil {
		return nil, fmt.Errorf("logic: grab status get order: %w", err)
	}
	if order == nil {
		// 受理中：消息在队列排队或消费者处理中，前端继续轮询
		return &model.StatusResult{Grabbed: true, OrderID: 0}, nil
	}
	// 3. 建单完成：轮询终点，前端拿 order_id 跳支付页
	return &model.StatusResult{Grabbed: true, OrderID: order.OrderID}, nil
}
