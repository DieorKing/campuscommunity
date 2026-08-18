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
	"time"
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
		GoodID:      snowflake.GenID(),
		PublisherID: publisherID,
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
	// 7. 发布成功，返回业务主键。
	return goodID, nil
}
