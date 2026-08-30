// Package logic 业务逻辑层：编排 DAO 与工具函数，实现拼单模块业务规则。
// 分层约束：不碰 HTTP（不 import gin），不写 SQL（数据操作全走 dao），业务错误用哨兵 error 上抛给 controller 翻译。
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/dao/redis"
	"campuscommunity/internal/model"
	"campuscommunity/pkg/utils/snowflake"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// 拼单模块哨兵错误：controller 用 errors.Is 识别并映射为业务响应码。
// 与用户模块同套路：logic 层对 HTTP 响应码无感知，返回哨兵 error，保持分层干净。
var (
	ErrGroupBuyInvalid  = errors.New("拼单参数不合法") // min>max 等交叉校验不通过
	ErrGroupBuyExpired  = errors.New("拼单已截止")   // deadline 在过去
	ErrGroupBuyNotExist = errors.New("拼单不存在")   // 详情/抢单查询不到（后续功能用）
)

// CreateGroupBuy 发布拼单：业务校验 → 雪花ID → 落库 → 初始化 Redis 库存（失败回滚）。
// 参数 publisherID 来自 JWT（当前登录用户），p 为已通过 binding 格式校验的请求体。
// 返回 (good_id, err)，good_id 是发布成功后对外的业务主键。
func CreateGroupBuy(publisherID int64, p *model.ParamCreateGroupBuy) (int64, error) {
	// 1. 业务交叉校验：min_members ≤ max_members。
	//    binding 只校验单个字段的格式，无法表达「两个字段的关系」，
	//    这类跨字段规则必须放在 logic 层。
	if p.MinMembers > p.MaxMembers {
		return 0, ErrGroupBuyInvalid
	}
	// 2. 截止时间校验：deadline 必须在未来。
	//    binding 的 required 只保证「传了」，不保证「合理」——
	//    传一个过去的时间也能通过 binding，这里拦截。
	if !p.Deadline.After(time.Now()) {
		return 0, ErrGroupBuyExpired
	}
	// 3. 组装拼单实体：业务主键 GoodID 用雪花算法生成（对外暴露、分布式唯一），
	//    发布者 publisherID 由 JWT 注入，状态默认 recruiting、参与人数默认 0（DB 默认值）。
	gb := &model.GroupBuy{
		GoodID:      model.ID(snowflake.GenID()),
		PublisherID: model.ID(publisherID),
		Title:       p.Title,
		Description: p.Description,
		Price:       p.Price,
		ImageURL:    p.ImageURL,
		MinMembers:  p.MinMembers,
		MaxMembers:  p.MaxMembers,
		Deadline:    p.Deadline,
	}
	// 4. 落库 MySQL：返回 good_id 供回显与后续 Redis 键使用。
	goodID, err := dao.CreateGroupBuy(gb)
	if err != nil {
		return 0, fmt.Errorf("logic: create group buy: %w", err)
	}
	// 5. 初始化 Redis 库存：stock = max_members，members = 空集合。
	//    这是后续抢单预扣库存的基础，必须在发布时一次性建好。
	if err := redis.InitGroupBuyStock(goodID, p.MaxMembers); err != nil {
		// 6. 补偿回滚：DB 写成功但 Redis 初始化失败 → 删除刚插入的 DB 记录。
		//    否则会出现「DB 有拼单、Redis 无库存」的不一致态，抢单无法进行。
		//    注意：回滚失败只记日志级别的错误，不覆盖原始错误（保证调用方看到真正原因）。
		if delErr := dao.DeleteGroupBuy(goodID); delErr != nil {
			return 0, fmt.Errorf("logic: init stock failed (%v), rollback also failed: %w", delErr, err)
		}
		return 0, fmt.Errorf("logic: init group buy stock: %w", err)
	}
	// 5.5 热榜入榜：ZADD hot:rank:group_buy <good_id> 0。
	//     失败策略（分层失败策略）：
	//     热榜是「展示数据」，允许短暂不准，不阻塞不回滚主流程——
	//     发布的核心是「DB 记录 + stock 库存」已成功，不能因排行榜牺牲发布。
	//     兜底：①查询时回表 MySQL 过滤非 recruiting 状态（不入榜不影响正确性）
	//          ②服务启动时从 DB 重建热榜（重建兜底）
	//     生产化升级路径：补偿消息投递（带 retry_count 的专用队列）。
	if err := redis.ZAddHotRank(goodID, 0); err != nil {
		// 只记错误日志，发布照常成功——数据分层：核心(库存/订单)原子，展示(热榜)最终一致
		zap.L().Error("logic: zadd hot rank failed, publish continues (best-effort)",
			zap.Int64("good_id", goodID), zap.Error(err))
	}
	// 7. 发布成功，返回业务主键。
	return goodID, nil
}

// GroupBuyDetail 拼单详情（GET /api/v1/group-buy/:id）：进页一次返回商品信息 + 进度 + 参与人员列表。
// userID 来自强制 JWT（该接口需登录），goodID 来自路径参数。
//
// 数据流（固定 3 次 DB 往返，与参与人数无关）：
//  1. dao.GetGroupBuyByID             查拼单主体     （1 次）
//  2. dao.ListGroupBuyMembersByGoodID 查全部成员     （1 次）
//  3. dao.GetUsersByUserIDs           IN 批查用户信息（1 次，防 N+1：循环逐个查 100 人=101 次往返）
//  4. 内存组装：map 索引 + is_joined/is_publisher 零成本推导（数据已在手，无需二次回源）
func GroupBuyDetail(userID, goodID int64) (*model.GroupBuyDetail, error) {
	// 1. 主查询：拼单不存在是业务分支（用户点了无效链接），翻译哨兵错误上抛，
	//    controller 映射 20001；DB 故障才是 err，走 ServerBusy。
	gb, err := dao.GetGroupBuyByID(goodID)
	if err != nil {
		return nil, fmt.Errorf("logic: get group buy detail: %w", err)
	}
	if gb == nil {
		return nil, ErrGroupBuyNotExist
	}
	// 2. 查全部参与记录：参与人数 ≤ max_members 天然有界，不分页一次拉全；按加入时间升序
	members, err := dao.ListGroupBuyMembersByGoodID(goodID)
	if err != nil {
		return nil, fmt.Errorf("logic: list group buy members: %w", err)
	}
	// 3. 收集 user_id → IN 批查用户（1 次往返代替 N 次）
	userIDs := make([]int64, 0, len(members))
	for i := range members {
		userIDs = append(userIDs, members[i].UserID.Int64())
	}
	// members 为空时 userIDs 是空切片，GetUsersByUserIDs 内部短路不发 SQL
	users, err := dao.GetUsersByUserIDs(userIDs)
	if err != nil {
		return nil, fmt.Errorf("logic: batch get users: %w", err)
	}
	// 4. map 索引：user_id → User（O(1) 取用）。IN 查询返回顺序不保证，
	//    组装时按 members 的时间顺序遍历，从 map 取用户——两份数据各管各的顺序。
	userMap := make(map[int64]model.User, len(users))
	for i := range users {
		userMap[users[i].UserID.Int64()] = users[i]
	}
	// 5. 组装参与人员列表：is_joined 在同一循环内顺手推导——
	//    成员列表已在手，「当前用户在不在里面」是一次内存比对，省掉一次 DB 查询。
	items := make([]model.MemberItem, 0, len(members))
	isJoined := false
	for i := range members {
		m := &members[i]
		if m.UserID.Int64() == userID {
			isJoined = true
		}
		u, ok := userMap[m.UserID.Int64()]
		if !ok {
			// members 引用的用户已不存在（脏数据/物理删除）——跳过该成员继续组装，
			// 不让一条脏数据把整个详情页打挂成 500。
			// 展示数据容忍缺失（对照核心数据：库存/订单绝不吞错）。
			continue
		}
		// 昵称兜底：nickname 可空（users 表非必填），空则回退 username，
		// 前端拿来即用不用判空——展示字段的边界处理归 logic。
		nickname := u.Nickname
		if nickname == "" {
			nickname = u.Username
		}
		items = append(items, model.MemberItem{
			UserID:   u.UserID,
			Nickname: nickname,
			Avatar:   u.Avatar,
			JoinedAt: m.CreatedAt, // 加入时刻 = 成员记录创建时刻
		})
	}
	// 6. 组装详情 DTO：is_publisher 同样是零成本比对（gb.PublisherID vs userID）。
	//    注意 Members 用 items（已剔除脏数据），保持 JSON 输出干净。
	return &model.GroupBuyDetail{
		GoodID:         gb.GoodID, // 同类型直传（ID → ID）
		Title:          gb.Title,
		Description:    gb.Description,
		Price:          gb.Price,
		ImageURL:       gb.ImageURL,
		MinMembers:     gb.MinMembers,
		MaxMembers:     gb.MaxMembers,
		CurrentMembers: gb.CurrentMembers,
		Deadline:       gb.Deadline,
		Status:         gb.Status,
		CreatedAt:      gb.CreatedAt,
		IsJoined:       isJoined,
		IsPublisher:    gb.PublisherID.Int64() == userID,
		Members:        items,
	}, nil
}

// normalizeListParam 列表参数归一化：page/page_size/sort 兜底到合法缺省值。
// 为什么要归一化而不是直接 binding 校验拒绝：
//   - 分页参数「没传」「传 0」「传负数」对用户语义相同（都用默认值），报参数错误反而苛刻；
//   - page_size 上限 50 是硬性上限（防止一次拉全表打爆 DB），超限必须压回 50 而非放行；
//   - sort 白名单防止拼接任意字符串（本实现走常量分支无注入风险，白名单是防御纵深）。
func normalizeListParam(p *model.ParamListGroupBuy) (page, pageSize int, sort string) {
	// page 归一化：< 1 一律视为第 1 页（含缺省 0）
	if p.Page < 1 {
		p.Page = 1
	}
	// page_size 归一化：< 1 用默认 10；> 50 压回 50（硬性上限，防大页拖库）
	if p.PageSize < 1 {
		p.PageSize = 10
	}
	if p.PageSize > 50 {
		p.PageSize = 50
	}
	// sort 白名单：只认 latest/hot，其余（含空串）一律 latest
	if p.Sort != "hot" {
		p.Sort = "latest"
	}
	return p.Page, p.PageSize, p.Sort
}

// ListGroupBuy 拼单列表：latest 走 MySQL 分页，hot 走 Redis ZSet 热榜 + 回表。
// userID 为当前登录用户（来自 JWTOptional 中间件）；0 表示未登录（is_joined 恒 false）。
// 两条路径的设计差异：
//   - latest：单数据源。COUNT + LIMIT/OFFSET 直接出结果，total 精确。
//   - hot：两段式。ZREVRANGE 出 Top N 个 good_id → MySQL IN 回表补详情 →
//     过滤非 recruiting → 按热榜顺序重排。total 用 ZCARD 近似（含终态未 ZREM 的成员）。
//
// 参与标记（登录后每项附加本人参与标记）：登录时一次批量查 members 表，
// map O(1) 判断填充 is_joined——不逐项查库（10 项/页若逐项查就是 10 次 DB 往返）。
func ListGroupBuy(userID int64, p *model.ParamListGroupBuy) (*model.ListResult, error) {
	page, pageSize, sort := normalizeListParam(p)

	// ---------- 分支一：latest（MySQL 直出） ----------
	if sort == "latest" {
		gbs, total, err := dao.ListGroupBuyPage(page, pageSize)
		if err != nil {
			return nil, fmt.Errorf("logic: list group buy latest: %w", err)
		}
		// 登录态：批量查参与集（一次 IN 查询覆盖当页全部拼单）
		joined, err := buildJoinedMap(userID, gbs)
		if err != nil {
			return nil, err
		}
		// model → 响应 DTO：剥离 description 大文本，控制 payload
		items := make([]model.GroupBuyItem, 0, len(gbs))
		for i := range gbs {
			item := toGroupBuyItem(&gbs[i], 0)
			item.IsJoined = joined[gbs[i].GoodID.Int64()]
			items = append(items, item)
		}
		return &model.ListResult{
			List:     items,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		}, nil
	}

	// ---------- 分支二：hot（Redis ZSet 热榜 + 回表） ----------
	// 1. ZREVRANGE 分页取 Top N（带 score）：拿到 good_id 列表 + 热度分
	zranks, err := redis.ZRevRangeHotRankPage(int64(page), int64(pageSize))
	if err != nil {
		return nil, fmt.Errorf("logic: list group buy hot zrevrange: %w", err)
	}
	if len(zranks) == 0 {
		// 榜空（或该页无成员）：total 也一并取 ZCARD 返回，前端据此终止翻页
		total, _ := redis.ZCardHotRank()
		return &model.ListResult{
			List: []model.GroupBuyItem{}, Total: total, Page: page, PageSize: pageSize,
		}, nil
	}
	// 2. 收集 good_id，准备回表。
	//    zMemberToInt64 做安全转换：Redis 读回的 member 是 string（见函数注释），
	//    直接 .(int64) 断言会 panic。
	goodIDs := make([]int64, 0, len(zranks))
	for _, z := range zranks {
		id, ok := zMemberToInt64(z.Member)
		if !ok {
			continue // 无法解析的脏数据：跳过（防御，正常不会出现）
		}
		goodIDs = append(goodIDs, id)
	}
	// 3. MySQL IN 批量回表（一次往返代替 N+1）
	gbs, err := dao.GetGroupBuysByIDs(goodIDs)
	if err != nil {
		return nil, fmt.Errorf("logic: list group buy hot backtrack: %w", err)
	}
	// 4. 过滤 + 按热榜顺序重排：
	//    - DB IN 查询返回顺序不保证，必须按 ZREVRANGE 的顺序重排；
	//    - 过滤非 recruiting（已终态但尚未 ZREM 的拼单不展示）。
	//    实现：map[good_id]→GroupBuy 建索引，再按热榜顺序遍历取用——O(N) 重排。
	gbMap := make(map[int64]model.GroupBuy, len(gbs))
	for i := range gbs {
		gbMap[gbs[i].GoodID.Int64()] = gbs[i]
	}
	// 登录态：批量查参与集（对回表结果整体查，含被过滤项也没关系——map 查询多几项无成本）
	joined, err := buildJoinedMap(userID, gbs)
	if err != nil {
		return nil, err
	}
	items := make([]model.GroupBuyItem, 0, len(zranks))
	for _, z := range zranks {
		id, ok := zMemberToInt64(z.Member)
		if !ok {
			continue
		}
		gb, ok := gbMap[id]
		if !ok {
			continue // DB 已删但 ZSet 残留（发布回滚未清理干净）——跳过，数据最终一致
		}
		// 过滤非 recruiting（已终态但尚未 ZREM 的拼单不展示）。
		if gb.Status != model.GroupBuyRecruiting {
			continue // 终态过滤：热榜只展示进行中的拼单（full/succeeded/failed/expired 均不展示）
		}
		item := toGroupBuyItem(&gb, z.Score)
		item.IsJoined = joined[gb.GoodID.Int64()]
		items = append(items, item)
	}
	// 5. total 用 ZCARD 近似（含终态，≥ 实际可见数；前端仅用于「有无下一页」判断）
	total, err := redis.ZCardHotRank()
	if err != nil {
		return nil, fmt.Errorf("logic: list group buy hot zcard: %w", err)
	}
	return &model.ListResult{
		List:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// buildJoinedMap 构建用户参与标记 map（未登录返回空 map，is_joined 自然全 false）。
// 统一封装：latest/hot 两条分支都要用，抽出来避免重复；
// 空列表短路——无数据时不发无意义的 DB 查询。
func buildJoinedMap(userID int64, gbs []model.GroupBuy) (map[int64]bool, error) {
	// 未登录（controller 传 userID=0）或列表为空：返回空 map，is_joined 自然全 false
	if userID <= 0 || len(gbs) == 0 {
		return map[int64]bool{}, nil
	}
	goodIDs := make([]int64, 0, len(gbs))
	for i := range gbs {
		goodIDs = append(goodIDs, gbs[i].GoodID.Int64())
	}
	joined, err := dao.GetUserJoinedGoodIDs(userID, goodIDs)
	if err != nil {
		return nil, fmt.Errorf("logic: build joined map: %w", err)
	}
	return joined, nil
}

// zMemberToInt64 把 ZSet 成员安全转换为 int64。
// ZADD 写入时 Member 传 int64，但读回来是 string——Redis 的数据模型一切皆字符串，
// 写入的类型信息不保留。用单返回值断言 z.Member.(int64) 会直接 panic（被 Recovery
// 拦成 500），必须用「type switch + ParseInt」双形式防御：
//   - int64：go-redis 某些版本/路径会直接给 int64（如 pipeline 内构造）
//   - string：从 Redis 服务端读回的标准形态，ParseInt 解析 "83092983636955136"
//
// 这也是类型断言的通用规范：边界处（外部数据进来）永远用双返回值 ok 形式，不裸断言。
func zMemberToInt64(member interface{}) (int64, bool) {
	switch v := member.(type) {
	case int64:
		return v, true
	case string:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return id, true
	default:
		return 0, false
	}
}

// toGroupBuyItem model.GroupBuy → 响应 DTO GroupBuyItem 的转换器。
// hotScore 仅 hot 排序有意义（latest 传 0）；转换集中在 logic 层，
// controller 拿到的就是最终 API 形状，不需要知道 model 长什么样。
func toGroupBuyItem(gb *model.GroupBuy, hotScore float64) model.GroupBuyItem {
	return model.GroupBuyItem{
		GoodID:         gb.GoodID,
		Title:          gb.Title,
		Price:          gb.Price,
		MinMembers:     gb.MinMembers,
		MaxMembers:     gb.MaxMembers,
		CurrentMembers: gb.CurrentMembers,
		ImageURL:       gb.ImageURL,
		Status:         gb.Status,
		Deadline:       gb.Deadline,
		HotScore:       hotScore,
		CreatedAt:      gb.CreatedAt,
	}
}
