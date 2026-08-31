// Package logic 业务逻辑层：编排 DAO 与工具函数，实现各模块业务规则。
// 本文件为通知模块在 logic 侧的统一挂载助手：所有业务事件的通知投递
// 都经由 notifyBestEffort 走统一模式——「挂尾部 + best-effort」。
// 六个挂载点（本包内分布）：
//   - order.go  CreateOrderByMessage 第5c步：订单待支付、第5d步：拼单已成团（批量）
//   - order.go  PayOrder 第4步：已支付
//   - order.go  CancelOrder 第5步：已取消
//   - delay.go  closeExpiredOrders：超时关闭
//   - delay.go  judgeExpiredGroupBuys：拼单失败/截止补翻成团（批量）
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/model"
	"campuscommunity/internal/mq"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// ErrNotificationNotExist 通知号查无此条（路径参数指错/数据不存在）。
// controller 用 errors.Is 映射 40001。
var ErrNotificationNotExist = errors.New("通知不存在")

// ErrNotificationNotOwner 通知存在但不属于当前用户（越权标记他人通知）。
var ErrNotificationNotOwner = errors.New("非本人通知")

// notifyBestEffort 通知投递的统一挂载姿势：失败仅记 error 日志，不返回错误、
// 不影响调用方主流程。
//
// 日志字段取自消息本身（收件人/细类/关联键）：足够定位「谁的那条通知丢了」，
// 不打全量 body（Title/Content 是展示文案，排障价值低）。
func notifyBestEffort(msg mq.NotificationMessage) {
	if err := mq.PublishNotification(msg); err != nil {
		// 投递失败：落补偿任务（重放型——payload 带完整消息，消费端
		// 唯一索引兜底重复投递）。落任务本身失败才降级纯日志
		//（补偿的补偿不存在，compensation.go 注释）
		zap.L().Error("logic: publish notification failed, compensating",
			zap.Int64("user_id", msg.UserID),
			zap.String("category", msg.Category),
			zap.Int64("ref_id", msg.RefID),
			zap.Error(err))
		CompensateNotification(msg)
	}
}

// notifyGroupBuyEvent 拼单类事件的批量通知：收件人 = 全体签到成员 + 发布者。
//
// 收件人语义（两个边界决策）：
//  1. 发布者单独补一条：他不能参团（业务红线），签到簿里没有他——
//     但成团/失败对发布者最相关（他发的单）。幂等键独立成立
//     (publisher, category, good_id)，与成员的互不冲突。
//  2. 已取消订单的成员也在收件人里：取消订单不删签到簿行（签到簿 =
//     「参与过」的记录，业务上取消只回退人数）。demo 容忍这点噪音；
//     精确版需按订单状态过滤收件人（JOIN orders），复杂度不值——
//     「展示容忍缺失/冗余，核心数据零容忍」的一贯取舍。
//
// 幂等双保险（与订单类通知同构）：
//  1. 发送侧选主：成团通知只由 BecameSucceeded 的唯一触发者发出；
//     截止判定通知只由逐行 CAS rows=1 的赢家发出——并发场景下全场只有一人投递
//  2. 落库侧唯一索引：即便发送侧重复（如建单时已发过成团、截止补翻又发），
//     (user, category, good_id) 撞索引静默 ack——同一事件每人最多一条
//
// 逐人独立 best-effort：一人投递失败不影响其余人（循环内各自吞错记日志）。
// 拉名单失败 = 整批无法投递，记日志丢弃（通知本就是易失数据）。
func notifyGroupBuyEvent(gb *model.GroupBuy, category model.NotificationCategory, title, content string) {
	// 拉签到簿收件人（有界：≤ max_members，一次查询）
	members, err := dao.ListGroupBuyMembersByGoodID(gb.GoodID.Int64())
	if err != nil {
		zap.L().Error("logic: list members for group buy notify failed, batch dropped (best-effort)",
			zap.Int64("good_id", gb.GoodID.Int64()), zap.Error(err))
		return
	}
	// map 去重防御：发布者理论上不在签到簿（不能参团），若未来规则变化
	// （如允许发布者参与），map 天然防重复收件
	recipients := make(map[int64]struct{}, len(members)+1)
	for i := range members {
		recipients[members[i].UserID.Int64()] = struct{}{}
	}
	recipients[gb.PublisherID.Int64()] = struct{}{}
	for uid := range recipients {
		notifyBestEffort(mq.NotificationMessage{
			UserID:   uid,
			Type:     string(model.NotifyGroupBuy),
			Category: string(category),
			RefID:    gb.GoodID.Int64(),
			Title:    title,
			Content:  content,
		})
	}
}

// ListNotifications 我的通知列表（GET /api/v1/notification/list）。
// 编排：分页归一化（与订单列表同规约）→ 列表 + 未读数两次查询。
// 未读数内嵌在响应里：前端 30s 轮询本接口一次，列表+角标同时刷新，
// 省一次独立轮询请求（读合并——轮询场景请求数就是成本）。
// 未读数查询失败不拖垮列表（角标是展示数据，容忍暂缺）：
// 仅记日志、Unread 返回 0，列表照常返回。
func ListNotifications(userID int64, p *model.ParamListNotification) (*model.NotificationListResult, error) {
	// 归一化：与 ListUserOrders 同规约（page 从 1 起，page_size 默认 10 上限 50）
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
	list, total, err := dao.ListNotificationsByUserPage(userID, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("logic: list notifications: %w", err)
	}
	// 未读角标 best-effort：失败仅记日志返回 0，不拖垮列表主体
	unread, err := dao.CountUnreadNotifications(userID)
	if err != nil {
		zap.L().Error("logic: count unread notifications failed, badge returns 0 (best-effort)",
			zap.Int64("user_id", userID), zap.Error(err))
		unread = 0
	}
	return &model.NotificationListResult{Total: total, Unread: unread, List: list}, nil
}

// MarkNotificationRead 标记单条通知已读（POST /api/v1/notification/:id/read）。
// 编排：前置查询（区分不存在/越权两种拒绝语义）→ 条件 UPDATE（已读幂等
// 落空视为成功）。与 PayOrder 同构的「查询为友好报错，守卫为绝对正确」双保险：
// 查询为了把 40001/越权/成功三种语义分清楚，UPDATE 的 WHERE user_id+is_read
// 守卫兜住查询间隙的竞态。
// 已读再标已读 = 幂等成功（rows=0 不报错）：前端重复点击/轮询重试的
// 第二击不该收到错误——「已读」是幂等事实，不是排他状态迁移。
func MarkNotificationRead(userID, notificationID int64) error {
	// 1. 前置查询：两种拒绝语义在这里分流（不存在 40001 / 越权 1003）
	n, err := dao.GetNotificationByID(notificationID)
	if err != nil {
		return fmt.Errorf("logic: mark read get notification: %w", err)
	}
	if n == nil {
		return ErrNotificationNotExist
	}
	if n.UserID.Int64() != userID {
		return ErrNotificationNotOwner
	}
	// 2. 条件 UPDATE：WHERE user_id AND is_read=false。
	//    rows=0 = 已读（前置查询刚过、间隙内被并发标记，或本就是已读态）
	//    ——幂等落空，统一视为成功，不返回错误
	ok, err := dao.MarkNotificationRead(notificationID, userID)
	if err != nil {
		return fmt.Errorf("logic: mark notification read: %w", err)
	}
	if !ok {
		// 已读幂等落空：不打日志（正常重试路径），静默成功
		return nil
	}
	return nil
}
