// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 本文件为订单模块的 MySQL 数据访问：
// /status 轮询查询 + MQ 消费者的幂等建单事务（四写合一）。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// mysqlDupEntryErrNum MySQL 重复键错误码（ER_DUP_ENTRY）。
// 服务器在 INSERT 违反唯一约束时返回该错误包，Go 驱动包装为
// *mysqldriver.MySQLError 结构体，其 Number 字段即此码。
const mysqlDupEntryErrNum = 1062

// ErrDuplicateEntry 唯一索引冲突的哨兵错误（INSERT 撞 uk_user_good）。
// 这是消费端幂等的判定依据：撞索引 = 该订单此前已建 = 眼前是重复投递
// 的消息（at-least-once 的正常代价），属确定性失败——消费者识别后直接
// ack，绝不 nack 重试（重试永远撞同一个索引，无限循环即毒消息）。
// 由 1062 错误码翻译而来，调用方用 errors.Is 判定；
// 驱动错误类型被留在 dao 层内部，上层不感知 MySQL 协议细节。
var ErrDuplicateEntry = errors.New("记录已存在")

// errCloseSkip 关单落空信号（已支付/已取消/订单不存在）：触发事务回滚，
// 事务外翻译为 closed=false——调用方据此移除任务，不再重试（包私有）。
var errCloseSkip = errors.New("dao: close skipped by state machine or not exist")

// OrderCreateResult 建单事务的结果报告：错误之外需要上抛的全部业务事实。
// 三个布尔各回答一个独立问题，调用方（logic）据此决定事务外的连带动作。
type OrderCreateResult struct {
	// BecameSucceeded 本事务是否为「成团触发者」。
	// 成团翻转的条件 UPDATE 返回 RowsAffected==1，意味着拼单状态
	// recruiting→succeeded 的迁移正好发生在本次 +1 之后——并发消费者中
	// 恰好只有一人能得到 1（其余人执行同语句时状态已非 recruiting，得 0），
	// 成团通知等连带动作只由这一人执行，无需任何显式选举代码。
	BecameSucceeded bool
	// BecameFull 本事务是否为「满员触发者」（succeeded→full 翻转的
	// RowsAffected==1）。min==max 的拼单会在同一事务内连跳两态，
	// 两个标志可同时为 true。
	BecameFull bool
	// Counted current_members 是否实际 +1。
	// recruiting/succeeded/full 三态均计数：成团≠截止，凑人继续到 max；
	// full 只是「不可再抢」的展示态，迟到消息是 Redis 预扣背书的合法占位
	// （预扣总数 ≤ max，Lua 库存守着，不会加超）。
	// 「人数冻结」只跟终局走（expired/failed），不跟满员走（full）。
	// false = 拼单行已处终态（expired/failed 之后才被消费的积压消息）：
	// 订单照建（消息代表 Redis 预扣成功的既成事实，用户需要订单闭环），
	// 但人数不再增长——盖棺定论的数字不被迟到者污染。
	// 终态行的人数滞后属可容忍的派生数据漂移，调用方记 warn 日志观察即可。
	Counted bool
}

// CreateOrderTx 消费者建单事务：订单、签到簿、人数、状态翻转四写合一。
//
// 为什么必须是同一个事务：这四行数据是「一个名额被占用」这一个业务
// 事实的四个侧面——订单（谁占的）、签到簿（占的凭证）、人数（占的进度）、
// 状态（占完的集体后果）。任何一件单独成功而其余失败，都会造成名额
// 错乱（例如订单在而人数没加 = 名额凭空多出，等价变相超卖），且这种
// 错乱无法从任何单一数据源重建。GORM Transaction 闭包返回 nil 即提交、
// 返回 error 即整体回滚，四写要么全部生效要么全部消失。
//
// 幂等入口在第一条 INSERT：重复消息撞 uk_user_good 唯一索引，事务回滚，
// 错误经 1062 判别翻译为 ErrDuplicateEntry 上抛。不在事务前做
// SELECT-then-INSERT 判重——「查」与「插」之间的间隙是 check-then-act
// 竞态，且唯一索引才是物理防线，前置 SELECT 只是白白多一次往返。
//
// 人数 +1 与状态翻转拆成多条 UPDATE 而非合并的原因：合并后 RowsAffected
// 恒为 1（行被 +1 改动即计数），无法区分「被 +1」与「被翻转」，选举信息
// 丢失——只有拆开，翻转语句的 RowsAffected 才是纯粹的选举结果。
//
// 并发安全机制（同一拼单的两个消费者并发到达时）：
//   - 写3 的 UPDATE 自带行锁，后到者在「+1」处阻塞，行锁持有至事务提交
//     ——并发消费者被天然串行化（悲观成分）；
//   - 后到者提交前重放翻转语句时，状态已被先到者改写，WHERE 条件不满足
//     得 RowsAffected=0，零成本落选且无需重试（乐观/CAS 成分）。
//
// 参数由 logic 层装配完毕传入（雪花 ID、金额/地址快照、pending_pay 初态），
// DAO 只负责落库，不做业务校验。
func CreateOrderTx(order *model.Order, member *model.GroupBuyMember) (*OrderCreateResult, error) {
	var res OrderCreateResult

	// 事务闭包：任一步失败整体回滚；成功路径填充 res 的三个布尔
	err := mysql.GetDB().Transaction(func(tx *gorm.DB) error {
		// 写1：订单存根（amount/address 是下单时刻快照，logic 层已装配）。
		// 重复消息在此撞唯一索引，错误直接返回触发整体回滚
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		// 写2：签到簿（good_id,user_id 复合唯一索引是成员侧的判重防线，
		// 与订单侧唯一索引构成双保险——Redis 去重失效时 DB 层兜底）
		if err := tx.Create(member).Error; err != nil {
			return err
		}

		// 写3：人数 +1。WHERE 限定「可计人」状态：expired/failed 终态行
		// rows=0 不计数（积压消息照建单但不涨人数，见 OrderCreateResult.Counted）。
		// gorm.Expr 生成 current_members = current_members + 1：
		// 自增必须由数据库计算，应用层读改写会引入竞态。
		// 本条 UPDATE 即并发串行化的起点：行锁从此持有到事务提交
		r := tx.Model(&model.GroupBuy{}).
			Where("good_id = ? AND status IN ?", order.GoodID,
				[]model.GroupBuyStatus{
					model.GroupBuyRecruiting,
					model.GroupBuySucceeded,
					model.GroupBuyFull,
				}).
			Update("current_members", gorm.Expr("current_members + 1"))
		if r.Error != nil {
			return r.Error
		}
		res.Counted = r.RowsAffected == 1

		// 写4a：成团翻转（recruiting→succeeded）。
		// 判定条件 current_members >= min_members 直接引用同表其他列，
		// 无需把 min_members 读进应用层比较——「查」被压进 WHERE，
		// check-then-act 的间隙在单条语句、一次行锁内消失（条件 UPDATE 的 CAS 语义）。
		// RowsAffected==1 = 全场唯一成团触发者
		r = tx.Model(&model.GroupBuy{}).
			Where("good_id = ? AND status = ? AND current_members >= min_members",
				order.GoodID, model.GroupBuyRecruiting).
			Update("status", model.GroupBuySucceeded)
		if r.Error != nil {
			return r.Error
		}
		res.BecameSucceeded = r.RowsAffected == 1

		// 写4b：满员翻转（succeeded→full，达到 max 后不可再抢）。
		// 与成团翻转同构的 CAS：只有恰好把人数推到上限的那次事务能翻转成功。
		// min==max 的拼单在同一事务内连跳两态（两标志同时为 true）
		r = tx.Model(&model.GroupBuy{}).
			Where("good_id = ? AND status = ? AND current_members >= max_members",
				order.GoodID, model.GroupBuySucceeded).
			Update("status", model.GroupBuyFull)
		if r.Error != nil {
			return r.Error
		}
		res.BecameFull = r.RowsAffected == 1

		return nil
	})
	if err != nil {
		// 1062 判别：errors.As 沿 %w 包装链逐层解包，穿透 GORM 与驱动的
		// 所有包装层找到 *mysqldriver.MySQLError。连接断开/超时等暂时性
		// 故障时服务器根本没有机会返回错误包（错误产生于网络层），
		// 类型不匹配自动落入下方通用分支——ack 与 nack 的二分天然成立。
		var me *mysqldriver.MySQLError
		if errors.As(err, &me) && me.Number == mysqlDupEntryErrNum {
			return nil, ErrDuplicateEntry
		}
		return nil, fmt.Errorf("dao: create order tx: %w", err)
	}
	return &res, nil
}

// GetOrderByUserAndGood 按用户 + 拼单查询订单（/status 轮询：抢到后查自己的订单号）。
// 返回约定同 GetUserByUsername：查不到返回 (nil, nil)——「订单尚未生成」是
// 正常业务分支（MQ 消费者还在处理，轮询的本义就是等它），由 logic 层
// 翻译为「受理中」；仅 DB 故障返回 err。
// 走 uk_user_good 复合唯一索引等值查询，单行返回。
// 幂等关联：(user_id, good_id) 唯一索引是消费端防重复建单的物理防线——
// 重复消息 INSERT 撞索引失败，消费者识别 ErrDuplicateEntry 后直接 ack 跳过。
func GetOrderByUserAndGood(userID, goodID int64) (*model.Order, error) {
	var order model.Order
	err := mysql.GetDB().Where("user_id = ? AND good_id = ?", userID, goodID).First(&order).Error
	// ErrRecordNotFound 是业务上的「还没建单」，就地消化，不让 logic 层 import gorm
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get order by user and good: %w", err)
	}
	return &order, nil
}

// GetOrderByOrderID 按业务主键查订单（订单详情/支付/取消的前置查询）。
// 返回约定同本包其他 Get：(nil, nil) = 不存在（正常业务分支，logic 翻译
// ErrOrderNotExist），仅 DB 故障返回 err。走 order_id 唯一索引等值查询。
func GetOrderByOrderID(orderID int64) (*model.Order, error) {
	var order model.Order
	err := mysql.GetDB().Where("order_id = ?", orderID).First(&order).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get order by order_id: %w", err)
	}
	return &order, nil
}

// UpdateOrderPaid 模拟支付（条件 UPDATE 状态机守卫）。
// UPDATE ... WHERE order_id=? AND user_id=? AND status='pending_pay'：
// 「查」压进 WHERE，双击支付的第二击、超时关单后的迟到支付、越权支付
// 全部被单条语句拒绝（状态机幂等防线，条件更新的惯用手法）。
// 返回 (true, nil) = 迁移成功；false = 状态机拒绝（含非本人/状态已变，
// 由 logic 前置查询区分语义）；err 仅 DB 故障。paid_at 由 MySQL NOW() 生成
// ——支付时间以数据库时钟为准，避免应用服务器时钟漂移。
func UpdateOrderPaid(orderID, userID int64) (bool, error) {
	r := mysql.GetDB().Model(&model.Order{}).
		Where("order_id = ? AND user_id = ? AND status = ?",
			orderID, userID, model.OrderPendingPay).
		Updates(map[string]any{
			"status":  model.OrderPaid,
			"paid_at": gorm.Expr("NOW()"),
		})
	if r.Error != nil {
		return false, fmt.Errorf("dao: update order paid: %w", r.Error)
	}
	return r.RowsAffected == 1, nil
}

// CancelOrderTx 用户取消订单（置 cancelled + current_members-1 同事务）。
// 两个表但同一个业务事实「一个名额被释放」——拆开会出现「订单 cancelled
// 但人数还占着」（名额凭空蒸发，反向超卖）或反之，必须同事务原子。
// 状态守卫：WHERE status='pending_pay' 挡住「已支付后取消」「双击取消」
// （幂等防线：状态机拒绝非法迁移）。人数守卫 current_members > 0 防御性防负。
// 返回 (goodID, true, nil) = 取消成功（goodID 供 logic 释放 Redis 名额）；
// (0, false, nil) = 状态机拒绝；err 仅 DB 故障。
func CancelOrderTx(orderID, userID int64) (int64, bool, error) {
	var goodID int64
	err := mysql.GetDB().Transaction(func(tx *gorm.DB) error {
		// 写1：条件 UPDATE 订单状态 pending_pay → cancelled。
		// 先 SELECT 本行拿 good_id（同时供事务内写2使用）——SELECT 与
		// UPDATE 之间理论上有竞态，但 UPDATE 的 WHERE 守卫兜底：状态
		// 被并发改掉则 rows=0，整体当作拒绝返回，绝无半提交。
		var order model.Order
		if err := tx.Where("order_id = ? AND user_id = ?", orderID, userID).
			First(&order).Error; err != nil {
			return err // ErrRecordNotFound 透传，事务外翻译为哨兵
		}
		goodID = order.GoodID
		r := tx.Model(&model.Order{}).
			Where("order_id = ? AND status = ?", orderID, model.OrderPendingPay).
			Update("status", model.OrderCancelled)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			// 状态机拒绝：整体回滚（写2 未执行），上层翻译 ErrOrderStatusChanged。
			// 用哨兵错误触发回滚——GORM Transaction 返回非 nil 即回滚。
			return errCancelRejected
		}
		// 写2：拼单人数 -1（同一业务事实的另一个侧面）。
		// 不回退成团状态：成团是既成事实（不做回退），人数滞后
		// 与「终态行不计数」同族容忍语义。
		r2 := tx.Model(&model.GroupBuy{}).
			Where("good_id = ? AND current_members > 0", goodID).
			Update("current_members", gorm.Expr("current_members - 1"))
		if r2.Error != nil {
			return r2.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errCancelRejected) {
			return 0, false, nil
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 订单不存在或非本人：统一交给 logic 前置语义（这里兜底返回拒绝）
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("dao: cancel order tx: %w", err)
	}
	return goodID, true, nil
}

// errCancelRejected CancelOrderTx 内部哨兵：状态机拒绝信号，触发事务回滚，
// 事务外翻译为 (false, nil) 返回——不对外暴露（包私有）。
var errCancelRejected = errors.New("dao: cancel rejected by state machine")

// CloseExpiredOrder 超时关单事务（置 closed + current_members-1 同事务）。
// 与 CancelOrderTx 同构：同一个业务事实「一个名额被释放」的两个侧面，
// 区别仅在触发方（延时扫描器 vs 用户）与终态（closed vs cancelled）。
// 入参只有 orderID（扫描器从 ZSet 拿到的就是它）；userID/goodID 由本函数
// 查出后返回，供调用方释放 Redis 名额（SREM members 需要 user_id）。
// 状态守卫 WHERE status='pending_pay'：已支付（29:59 完成支付的竞态赢家）/
// 已取消的订单 rows=0 落空，整体无副作用——与支付的条件 UPDATE 互为
// 「行锁 + WHERE 守卫」双保险的对手方。
// 返回：(goodID, userID, true, nil) = 关单成功；(0, 0, false, nil) =
// 状态机落空或订单不存在；err 仅 DB 故障（调用方保留任务下轮重试）。
func CloseExpiredOrder(orderID int64) (goodID, userID int64, closed bool, err error) {
	err = mysql.GetDB().Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Where("order_id = ?", orderID).First(&order).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errCloseSkip // 订单不存在：无单可关，事务外视为落空
			}
			return err
		}
		// 写1：条件 UPDATE 关单（closed_at 取 DB 时钟，同 paid_at 的口径）
		r := tx.Model(&model.Order{}).
			Where("order_id = ? AND status = ?", orderID, model.OrderPendingPay).
			Updates(map[string]any{
				"status":    model.OrderClosed,
				"closed_at": gorm.Expr("NOW()"),
			})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return errCloseSkip // 已支付/已取消：竞态输家，落空退出
		}
		// 写2：拼单人数 -1（同一业务事实的另一侧面，与取消路径同构）。
		// 成团状态不回退：既成事实结论不被迟到事件改写
		r2 := tx.Model(&model.GroupBuy{}).
			Where("good_id = ? AND current_members > 0", order.GoodID).
			Update("current_members", gorm.Expr("current_members - 1"))
		if r2.Error != nil {
			return r2.Error
		}
		goodID, userID = order.GoodID, order.UserID
		return nil
	})
	if err != nil {
		if errors.Is(err, errCloseSkip) {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("dao: close expired order: %w", err)
	}
	return goodID, userID, true, nil
}



// ListOrdersByUserPage 按用户分页查询订单（按用户、状态筛选，分页）。
// status 为空 = 全部状态；非空时精确匹配（user_id 有索引，组合
// (user_id, status) 过滤量级为用户订单数，无需复合索引——个人订单有界）。
// 两条 SQL：COUNT 总数 + LIMIT/OFFSET 当前页（与 ListGroupBuyPage 同构）。
// 按 id 倒序 = 建单时间倒序（最新订单在前）。
func ListOrdersByUserPage(userID int64, status model.OrderStatus, page, pageSize int) ([]model.Order, int64, error) {
	var (
		list  []model.Order
		total int64
	)
	db := mysql.GetDB().Model(&model.Order{}).Where("user_id = ?", userID)
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("dao: count orders: %w", err)
	}
	if total == 0 {
		return list, 0, nil
	}
	offset := (page - 1) * pageSize
	if err := db.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("dao: list orders page: %w", err)
	}
	return list, total, nil
}
