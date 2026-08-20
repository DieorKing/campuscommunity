// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 本文件为拼单模块的 MySQL 数据访问，供 logic/group_buy.go 调用。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"fmt"

	"gorm.io/gorm"
)

// CreateGroupBuy 新增一条拼单记录（发布拼单时调用）。
// 入参是已由 logic 层填充好的 *model.GroupBuy：
//   - GoodID：雪花算法生成，logic 层赋值
//   - PublisherID：当前登录用户 user_id，logic 层赋值
//   - Status：默认 recruiting、CurrentMembers 默认 0（由 GORM 默认值处理）
//
// 返回 (good_id, err)：good_id 供 HTTP 层回显给前端；插入失败返回包装错误。
// DAO 不做任何业务校验（如 title 长度、deadline 合法性），那些属于 logic 层职责。
func CreateGroupBuy(g *model.GroupBuy) (int64, error) {
	if err := mysql.GetDB().Create(g).Error; err != nil {
		return 0, fmt.Errorf("dao: create group buy: %w", err)
	}
	// 创建成功后 g.ID（内部自增主键）与 g.GoodID（业务主键）都被 GORM 回填，
	// 返回业务主键给上层用于对外暴露。
	return g.GoodID, nil
}

// DeleteGroupBuy 按业务主键删除拼单记录（发布流程中 Redis 初始化失败的补偿回滚）。
// 为什么需要：DB 写成功、Redis 初始化失败时，若不删除 DB 记录，
// 会出现「DB 有拼单、Redis 无库存」的不一致态，后续抢单无法进行。
// 用物理删除（Unscoped 不生效，表无软删字段）直接删行，简单可靠。
// 硬删除需谨慎：本函数仅用于创建后立即回滚（无子记录），非业务删除场景。
func DeleteGroupBuy(goodID int64) error {
	result := mysql.GetDB().Where("good_id = ?", goodID).Delete(&model.GroupBuy{})
	if result.Error != nil {
		return fmt.Errorf("dao: delete group buy: %w", result.Error)
	}
	return nil
}

// ListGroupBuyPage 按创建时间倒序分页查询拼单列表（latest 排序，契约 mvp §2.4）。
// page 从 1 开始（由 logic 层归一化，DAO 只信任入参）；total 为全量计数（前端显示总数）。
// 不过滤 status：latest 列表展示所有拼单（含已成团/失败），前端用状态徽章区分——
// 与 hot 榜「只展示 recruiting」语义不同，hot 的过滤在 logic 层回表后做。
// 两条 SQL：COUNT 拿总数 + LIMIT/OFFSET 拿当前页，无 JOIN（列表页无关联查询需求）。
func ListGroupBuyPage(page, pageSize int) ([]model.GroupBuy, int64, error) {
	var (
		list  []model.GroupBuy
		total int64
	)
	db := mysql.GetDB().Model(&model.GroupBuy{})
	// 先 COUNT：total=0 时可直接短路返回空列表，省一次分页查询
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("dao: count group buy: %w", err)
	}
	if total == 0 {
		return list, 0, nil
	}
	// OFFSET = (page-1)*pageSize：page 已从 1 归一化，page=1 时 OFFSET=0
	offset := (page - 1) * pageSize
	if err := db.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("dao: list group buy page: %w", err)
	}
	return list, total, nil
}

// GetGroupBuysByIDs 按业务主键批量查询拼单（hot 榜回表，契约 mvp §2.4）。
// 热榜 ZREVRANGE 只能拿到 good_id+score，商品详情需回 MySQL 补全——
// IN 单次往返代替 N 次单查（N+1 问题），这是「缓存持 ID、DB 补详情」的标准两段式。
// 返回顺序不保证：DB IN 查询按存储顺序返回，logic 层需按热榜顺序重排。
func GetGroupBuysByIDs(goodIDs []int64) ([]model.GroupBuy, error) {
	var list []model.GroupBuy
	if err := mysql.GetDB().Where("good_id IN ?", goodIDs).Find(&list).Error; err != nil {
		return nil, fmt.Errorf("dao: get group buys by ids: %w", err)
	}
	return list, nil
}

// GetGroupBuyByID 按业务主键查询单条拼单（详情页主查询，后续抢单/状态接口也会复用）。
// 返回约定与 GetUserByUsername 一致：不存在返回 (nil, nil)——
// 「查无此单」是正常业务分支（用户点了不存在的链接/拼单已被回滚删除），
// 由 logic 层翻译为 ErrGroupBuyNotExist 哨兵错误上抛；
// 仅 DB 真正故障（连接断、超时）才返回 err，上层统一走 ServerBusy。
// good_id 上有 uniqueIndex，等值查询走索引，O(logN) 单行返回。
func GetGroupBuyByID(goodID int64) (*model.GroupBuy, error) {
	var gb model.GroupBuy
	err := mysql.GetDB().Where("good_id = ?", goodID).First(&gb).Error
	// ErrRecordNotFound 是「业务上的不存在」而非「技术上的故障」，在这里就地消化，
	// 不让 logic 层 import gorm——保持分层的意义正在于此：dao 层翻译 ORM 语义。
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get group buy by id: %w", err)
	}
	return &gb, nil
}

// ListGroupBuyMembersByGoodID 查询拼单的全部参与记录（详情页「参与人员」数据源）。
// ORDER BY created_at ASC：按加入时间先后展示（先来先显示）。
// 为什么必须显式排序：不指定 ORDER BY 时 MySQL 不保证返回顺序（受存储引擎/执行计划影响），
// 同一详情页刷新几次人员列表会"跳动"，前端无法做稳定渲染/动画。
// 为什么不分页：参与人数 ≤ max_members（发布时校验的上限，个位数量级、有界），
// 一次拉全开销可控；分页是给无限增长数据（评论/订单流）设计的，这里有界数据分页属过度设计。
// 走 (good_id, user_id) 复合唯一索引的最左前缀，等值查询索引高效。
func ListGroupBuyMembersByGoodID(goodID int64) ([]model.GroupBuyMember, error) {
	var members []model.GroupBuyMember
	if err := mysql.GetDB().Where("good_id = ?", goodID).
		Order("created_at ASC").Find(&members).Error; err != nil {
		return nil, fmt.Errorf("dao: list group buy members: %w", err)
	}
	return members, nil
}

// GetUserJoinedGoodIDs 查询用户在给定拼单集合中已参与的 good_id 集合。
// 用途：列表/详情接口的「本人参与标记」（契约：登录后每项附加参与标记）。
// 返回 map[good_id]bool 而非 []int64：调用方组装列表时 O(1) 判断某拼单是否参与，
// 避免对每个列表项做线性 contains（10 条/页无感，但写法上 map 是标准做法）。
// goodIDs 为空时短路返回空 map——IN () 空切片在 GORM 中生成非法 SQL，必须防御。
func GetUserJoinedGoodIDs(userID int64, goodIDs []int64) (map[int64]bool, error) {
	// goodIDs 为空时短路返回空 map——IN () 空切片在 GORM 中生成非法 SQL，必须防御。
	joined := make(map[int64]bool, len(goodIDs))
	if len(goodIDs) == 0 {
		return joined, nil
	}
	// 只查 good_id 一列：参与标记只需要 ID 集合，Pluck 生成 SELECT good_id FROM ...，不拉整行
	var ids []int64
	if err := mysql.GetDB().Model(&model.GroupBuyMember{}).
		Where("user_id = ? AND good_id IN ?", userID, goodIDs).
		Pluck("good_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("dao: get user joined good ids: %w", err)
	}
	// 已参与的置 true，未参与的缺省 false
	for _, id := range ids {
		joined[id] = true
	}
	return joined, nil
}
