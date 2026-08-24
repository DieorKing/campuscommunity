// Package redis 数据访问层：Redis 缓存/库存操作，不含业务逻辑。
// 本文件为拼单热榜 ZSet 操作。
// 热榜规则：member=good_id，score=热度分；发布 ZADD 0、建单成功 ZINCRBY +1、
// 查询 ZREVRANGE+回表过滤、定时衰减、终态 ZREM、启动重建。
package redis

import (
	"fmt"
	"strconv"

	"github.com/go-redis/redis"
)

// hotRankKey 拼单热度榜键：hot:rank:group_buy（全局唯一键，非按拼单维度）。
// 所有拼单共用一个 ZSet，靠 score 排序取出 Top N。
func hotRankKey() string {
	return "hot:rank:group_buy"
}

// ZAddHotRank 发布拼单时入榜（score=0）。
// 语义：新拼单初始热度为 0，排在榜尾；后续建单成功由 ZINCRBY 累加。
// ZADD 是「新增或更新」语义（member 已存在则覆盖 score）——发布场景 good_id 唯一，无覆盖风险。
// 失败处理：best-effort——logic 层只记 error 日志不回滚（展示数据不阻塞主流程）。
func ZAddHotRank(goodID int64, score float64) error {
	if err := client.ZAdd(hotRankKey(), redis.Z{Score: score, Member: goodID}).Err(); err != nil {
		return fmt.Errorf("redis: zadd hot rank: %w", err)
	}
	return nil
}

// ZIncrHotRank 建单成功时热度 +1（每有一单建成 ZINCRBY +1，
// 挂在建单分支而非成团分支——成团是全团一次，建单是每人一次）。
// ZINCRBY 对不存在的 member 自动以 0 起步再加分，无需预 ZADD（防漏）；
// best-effort：失败仅记日志，热榜可从订单表重建（展示数据不阻塞主流程）。
func ZIncrHotRank(goodID int64) error {
	// ZIncrBy 的 member 只收 string（go-redis v6 API 类型不对称：ZADD 收
	// interface{} 而 ZIncrBy 收 string——ZADD 写入的 int64 member 在此必须
	// 字符串化，与既有教训「写入读取两端类型口径必须一致」同源）。
	if err := client.ZIncrBy(hotRankKey(), 1, strconv.FormatInt(goodID, 10)).Err(); err != nil {
		return fmt.Errorf("redis: zincrby hot rank: %w", err)
	}
	return nil
}

// ZRevRangeHotRankPage 热榜降序分页读取（查询侧）。
// ZREVRANGE 按 score 从高到低返回 [start, stop] 闭区间成员，带 score 供前端展示热度。
// 分页数学：page=1,page_size=10 → ZREVRANGE 0 9；page=2 → ZREVRANGE 10 19
//
//	start = (page-1)*page_size，stop = start + page_size - 1（ZREVRANGE 的 stop 是含端点）
//
// 注意：offset 分页在「回表过滤终态」后页内条数会少于 page_size，由 logic 层
//
//	取 Top N+buffer 策略补偿（DAO 不管业务过滤，只提供原始分页读取）。
func ZRevRangeHotRankPage(page, pageSize int64) ([]redis.Z, error) {
	start := (page - 1) * pageSize
	stop := start + pageSize - 1
	result, err := client.ZRevRangeWithScores(hotRankKey(), start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: zrevrange hot rank page: %w", err)
	}
	return result, nil
}

// ZCardHotRank 热榜成员总数（分页 total 近似值）。
// ZCARD O(1) 拿 ZSet 基数。为什么是「近似」：total 含终态拼单（尚未 ZREM），
// 回表过滤后实际可见数 ≤ 该值；前端只用于「加载更多」的终止判断，无需精确。
func ZCardHotRank() (int64, error) {
	total, err := client.ZCard(hotRankKey()).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: zcard hot rank: %w", err)
	}
	return total, nil
}
