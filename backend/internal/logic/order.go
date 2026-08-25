// Package logic 业务逻辑层：编排 DAO 与工具函数，实现订单模块业务规则。
// 本文件为 MQ 消费者建单编排（建单流程第 1-6 步在
// CreateOrderByMessage 完成，第 7 步 ack/nack 由 mq 消费者根据返回错误分类执行）。
// 分层约束：不碰 HTTP、不碰 AMQP 协议——本函数只编排 dao/redis 调用顺序，
// 错误以哨兵形式上抛，由 mq 层翻译为 ack/nack 决策。
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/model"
	"campuscommunity/pkg/utils/snowflake"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// 订单模块哨兵错误：mq 消费者用 errors.Is 识别。
// 三者共同点：均为「确定性失败」——重试永远不会成功（消息内容决定的
// 事实，不随时间改变），消费者识别后必须直接 ack（nack 重投只会撞同
// 一堵墙，无限循环即毒消息）。
var (
	// ErrGoodNotExist 建单消息里的 good_id 在 DB 查不到（拼单已被删除/
	// 数据损坏）。消费者侧唯一跳过建单的情形。
	ErrGoodNotExist = errors.New("拼单不存在")
	// ErrUserNotExist 建单消息里的 user_id 在 DB 查不到（用户被物理删除）。
	// 与 ErrGoodNotExist 同性质：订单的 owner 不存在，建单无意义。
	ErrUserNotExist = errors.New("用户不存在")
)

// 订单 HTTP 接口哨兵错误：controller 用 errors.Is 映射为 30xxx 业务码。
var (
	// ErrOrderNotExist 订单号查无此单（路径参数指错/数据不存在）。
	ErrOrderNotExist = errors.New("订单不存在")
	// ErrOrderNotOwner 订单存在但不属于当前用户（越权访问他人订单）。
	ErrOrderNotOwner = errors.New("非本人订单")
	// ErrOrderStatusChanged 状态机拒绝：订单当前状态不允许本次迁移
	// （双击支付的第二击 / 已支付后取消 / 已取消后取消）——状态机幂等
	// 防线在支付/取消路径的落点。
	ErrOrderStatusChanged = errors.New("订单状态已变更")
)

// CreateOrderByMessage 消费者建单编排：一条 {good_id, user_id} 消息 →
// 反查快照 → 装配 → 事务四写 → 事务外 best-effort 连带动作。
//
// 编排顺序（建单流程的 logic 侧展开）：
//  1. 反查拼单：拿 amount 快照（price），判定 good 存在性（不存在→
//     确定性失败上抛，消费者 ack + error 日志）。
//     注意：不校验拼单状态/截止时间——消息代表 Redis 预扣成功的既成
//     事实（抢单入口已校验 deadline），终态拼单照建单（不建则
//     用户永久卡在受理中，且 pending 补偿扫描会死循环）。
//  2. 反查用户：拿 address 快照。消息只带 id 不带状态（事件不是状态，
//     权威在 DB）；查询发生在冷路径（削峰后消费者），
//     微秒级主键查询花得起。
//  3. 装配 order（雪花 order_id + amount/address 快照 + pending_pay 初态）
//     与 member（签到簿行，member_id 同为雪花）。
//  4. dao.CreateOrderTx 四写合一（订单+签到簿+人数+状态翻转，同一事务）。
//     撞 uk_user_good → dao.ErrDuplicateEntry 原样上抛（消费者翻译为
//     「重复消息的成功证明」→ ack）。
//  5. 事务外 best-effort：ZINCRBY 热榜 / ZADD 延时关单（通知投递为
//     本编排的既定扩展点，挂在同一位置）。
//     派生数据失败仅记 error 日志，绝不回滚已提交的事务、绝不让消费者
//     nack（重建成本低：热榜可从订单表重算，延时任务有补偿兜底）。
//
// 返回值：三个布尔来自事务结果（成团/满员触发者、是否计数），供消费者
// 日志埋点与成团通知使用；error 为 nil 即建单成功（含重复消息——
// ErrDuplicateEntry 由消费者单独识别，语义是「已处理过」而非失败）。
func CreateOrderByMessage(goodID, userID int64) (*dao.OrderCreateResult, error) {
	// ---- 第 1 步：反查拼单（存在性 + 价格快照） ----
	gb, err := dao.GetGroupBuyByID(goodID)
	if err != nil {
		// DB 故障 = 暂时性失败：上抛包装错误，消费者 nack 重试
		return nil, fmt.Errorf("logic: create order get group buy: %w", err)
	}
	if gb == nil {
		// good 不存在 = 确定性失败：消费者 ack + error 日志（唯一跳过建单的情形）
		return nil, ErrGoodNotExist
	}

	// ---- 第 2 步：反查用户（地址快照） ----
	user, err := dao.GetUserByUserID(userID)
	if err != nil {
		return nil, fmt.Errorf("logic: create order get user: %w", err)
	}
	if user == nil {
		// 用户不存在 = 确定性失败：无主的订单没有意义
		return nil, ErrUserNotExist
	}

	// ---- 第 3 步：装配订单与签到簿（纯内存操作，无 IO） ----
	order := &model.Order{
		OrderID: snowflake.GenID(),     // 业务主键（订单号，对外暴露）
		UserID:  userID,                // 订单归属者
		GoodID:  goodID,                // 关联拼单
		Amount:  gb.Price,              // 金额快照：下单时刻拼单价（存根语义，发布后不可改）
		Address: user.Address,          // 地址快照：用户当前地址（下单时地址的正解）
		Status:  model.OrderPendingPay, // 初始状态：待支付（30min 不付→closed）
	}
	member := &model.GroupBuyMember{
		MemberID: snowflake.GenID(), // 签到簿行主键（雪花，与 order_id 各自独立生成）
		GoodID:   goodID,
		UserID:   userID,
	}

	// ---- 第 4 步：事务四写合一（幂等入口：撞唯一索引 → ErrDuplicateEntry） ----
	res, err := dao.CreateOrderTx(order, member)
	if err != nil {
		// ErrDuplicateEntry 原样上抛：消费者识别后 ack（重复消息的成功证明）。
		// 其余错误（DB 断连等暂时性故障）同样上抛：消费者 nack 退避重试。
		// 两类错误的分拣点是消费者，logic 不吞不错译
		return nil, err
	}
	// 积压消息容忍语义：终态拼单照建单但不计数（人数冻结跟终局走，
	// 不跟满员走），记 warn 供对账观察
	if !res.Counted {
		zap.L().Warn("logic: order created on terminal group buy, members not counted",
			zap.Int64("good_id", goodID), zap.Int64("user_id", userID),
			zap.Int64("order_id", order.OrderID))
	}

	// ---- 第 5 步：事务外 best-effort（派生数据，失败仅记日志） ----
	// 5a. 热榜 +1：每单建成 +1（非成团才加——成团是全团一次，建单是每人一次）
	if err := redis.ZIncrHotRank(goodID); err != nil {
		zap.L().Error("logic: incr hot rank failed, order kept (best-effort)",
			zap.Int64("good_id", goodID), zap.Error(err))
	}
	// 5b. 延时关单入队：now+30min 到期，延时扫描器捞取关闭
	if err := redis.EnqueueOrderClose(order.OrderID); err != nil {
		zap.L().Error("logic: enqueue order close failed, order kept (best-effort)",
			zap.Int64("order_id", order.OrderID), zap.Error(err))
	}
	// 5c. 通知投递扩展点：订单待支付 +（若 res.BecameSucceeded）拼单已成团
	//     BecameSucceeded 的 RowsAffected 选主保证成团通知全场只发一次

	return res, nil
}

// PayOrder 模拟支付（POST /api/v1/order/:id/pay，条件 UPDATE 状态机守卫）。
// 编排：前置查询（区分不存在/越权/状态已变三种拒绝语义）→ 条件 UPDATE
// 迁移状态 → 事务外 best-effort 移除延时关单任务。
// 前置查询与 UPDATE 之间的竞态由 UPDATE 的 WHERE 守卫兜底（rows=0 统一
// 翻译为状态已变更）——「查询为了友好报错，守卫为了绝对正确」双保险。
func PayOrder(userID, orderID int64) error {
	// 1. 前置查询：三种拒绝语义在这里分流（不存在 30001 / 越权 1003 / 状态 30002）
	order, err := dao.GetOrderByOrderID(orderID)
	if err != nil {
		return fmt.Errorf("logic: pay get order: %w", err)
	}
	if order == nil {
		return ErrOrderNotExist
	}
	if order.UserID != userID {
		return ErrOrderNotOwner
	}
	if order.Status != model.OrderPendingPay {
		return ErrOrderStatusChanged
	}
	// 2. 条件 UPDATE：pending_pay → paid + paid_at=NOW()（状态机守卫，
	//    双击/迟到/越权在此被物理拒绝——即使前置查询后被并发改状态也安全）
	ok, err := dao.UpdateOrderPaid(orderID, userID)
	if err != nil {
		return fmt.Errorf("logic: pay update order: %w", err)
	}
	if !ok {
		return ErrOrderStatusChanged
	}
	// 3. best-effort：移除延时关单任务（ZREM）。失败无害的闭环设计：
	//    30min 后扫描器关单也是条件 UPDATE（仅 pending_pay→closed），
	//    撞上已 paid 的订单 rows=0 落空——残留任务被状态机自然免疫，
	//    无需补偿。此处失败仅记日志。
	if err := redis.RemoveOrderClose(orderID); err != nil {
		zap.L().Error("logic: remove order close task failed, harmless (state machine guards)",
			zap.Int64("order_id", orderID), zap.Error(err))
	}
	// 4. 通知投递扩展点：「已支付」
	return nil
}

// CancelOrder 用户取消订单（POST /api/v1/order/:id/cancel）。
// 编排：前置查询分流语义 → 取消事务（cancelled + 人数-1 原子）→
// 事务提交后 best-effort 释放 Redis 名额 + 移除延时任务。
// 释放顺序铁律：DB 真值源先行，Redis 派生数据随后——反序则 DB 取消
// 失败时名额已放 = 超卖窗口（ReleaseGroupBuySlot 注释同源）。
func CancelOrder(userID, orderID int64) error {
	// 1. 前置查询：同 PayOrder 的三分流
	order, err := dao.GetOrderByOrderID(orderID)
	if err != nil {
		return fmt.Errorf("logic: cancel get order: %w", err)
	}
	if order == nil {
		return ErrOrderNotExist
	}
	if order.UserID != userID {
		return ErrOrderNotOwner
	}
	if order.Status != model.OrderPendingPay {
		return ErrOrderStatusChanged
	}
	// 2. 取消事务：cancelled + current_members-1 原子（同一业务事实的
	//    两个侧面必须同事务）；状态守卫挡双击取消/已支付取消
	goodID, ok, err := dao.CancelOrderTx(orderID, userID)
	if err != nil {
		return fmt.Errorf("logic: cancel order tx: %w", err)
	}
	if !ok {
		return ErrOrderStatusChanged
	}
	// 3. best-effort：释放 Redis 名额（INCR stock + SREM members）。
	//    失败后果：少卖方向（名额没放出去/该用户被 DUPLICATE 误拦），
	//    不超卖，记日志人工/补偿处理
	if err := redis.ReleaseGroupBuySlot(goodID, userID); err != nil {
		zap.L().Error("logic: release group buy slot failed (under-sell direction, tolerable)",
			zap.Int64("good_id", goodID), zap.Int64("user_id", userID), zap.Error(err))
	}
	// 4. best-effort：移除延时关单任务。同 PayOrder 的无害闭环：
	//    残留任务撞 cancelled 订单 rows=0 自然落空
	if err := redis.RemoveOrderClose(orderID); err != nil {
		zap.L().Error("logic: remove order close task failed, harmless (state machine guards)",
			zap.Int64("order_id", orderID), zap.Error(err))
	}
	// 5. 通知投递扩展点：「已取消」
	return nil
}

// ListUserOrders 我的订单列表（GET /api/v1/order/list）。
// 分页归一化与拼单列表同规约（page 从 1 起，page_size 默认 10 上限 50）。
func ListUserOrders(userID int64, p *model.ParamListOrder) (*model.OrderListResult, error) {
	// 归一化：零值/负值回落默认；上限 50 防大页拖库（工程约定）
	page, pageSize := p.Page, p.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 50 {
		pageSize = 50
	}
	list, total, err := dao.ListOrdersByUserPage(userID, model.OrderStatus(p.Status), page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("logic: list user orders: %w", err)
	}
	return &model.OrderListResult{Total: total, List: list}, nil
}

// GetOrderDetail 订单详情（GET /api/v1/order/:id）。
// 所有权校验：订单是私产（含地址/金额快照），非本人 403 语义——
// 与「详情页参与人员」公开语义不同，这里是资源所有者校验的标准位。
func GetOrderDetail(userID, orderID int64) (*model.Order, error) {
	order, err := dao.GetOrderByOrderID(orderID)
	if err != nil {
		return nil, fmt.Errorf("logic: get order detail: %w", err)
	}
	if order == nil {
		return nil, ErrOrderNotExist
	}
	if order.UserID != userID {
		return nil, ErrOrderNotOwner
	}
	return order, nil
}
