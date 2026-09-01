// Package ratelimit 限流中间件：令牌桶算法，按用户维度精细限流。
//
// 模式说明（Limiter 接口抽象）：
// 接口为分布式演进预留——多实例部署时限流计数需跨实例共享，届时
// 提供 Redis+Lua 实现（脚本内原子完成「补令牌+扣令牌」）替换内存桶，
// 挂载代码零改动。当前单实例部署选择内存桶：单机无跨实例共享计数
// 需求，Redis 桶反而引入每请求一跳网络往返。
//
// 限流 key 设计（场景驱动）：按 userID 而非 IP——校园网 NAT 出口下
// 数千学生共享少数公网 IP，按 IP 限流会误伤共享出口的合法用户；
// userID 由 JWT 中间件解出（本中间件挂在 JWT 之后），精确到人。
// 公开接口（无 userID）不适用本中间件，留给网关层做粗粒度防护。
package ratelimit

import (
	"sync"
	"time"
)

// Limiter 限流器抽象：判断 key 当前是否允许放行。
// 实现方必须保证并发安全（gin 并发请求共享同一实例）。
type Limiter interface {
	// Allow 返回 true = 放行（消耗一个令牌）；false = 限流拒绝。
	Allow(key string) bool
}

// memoryBucket 单 key 的令牌桶状态（MemoryLimiter 内部持有）。
// 惰性补充设计：无后台 goroutine 定时投令牌——Allow 被调用时按
// 距上次调用的时间差一次性补足（tokens += elapsed × rate，封顶
// capacity）。长期无流量的桶不消耗任何资源（无定时器空转），
// 补充精度由调用时刻决定，对秒级限流精度足够。
type memoryBucket struct {
	tokens   float64   // 当前令牌数（浮点支持小数速率，如 0.5 个/秒）
	lastTime time.Time // 上次补充时刻（单调时钟语义，取 time.Now）
}

// MemoryLimiter 内存令牌桶限流器：每 key 独立桶。
// 字段全并发安全：mu 保护 buckets map 与各桶内部状态。
type MemoryLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*memoryBucket
	rate     float64 // 令牌补充速率（个/秒）
	capacity float64 // 桶容量（允许的最大突发量）
}

// NewMemoryLimiter 创建内存令牌桶限流器。
// rate：稳态放行速率（个/秒）；capacity：桶容量（突发上限）。
// 参数关系：容量 > 速率意味着允许短时突发后回落稳态；容量 = 速率
// 则退化为匀速。参数取值依据见调用方（router.go）注释。
func NewMemoryLimiter(rate float64, capacity float64) *MemoryLimiter {
	return &MemoryLimiter{
		buckets:  make(map[string]*memoryBucket),
		rate:     rate,
		capacity: capacity,
	}
}

// Allow 判断 key 是否放行（惰性补充 + 消耗一个令牌）。
// 锁粒度说明：全局单锁而非分桶锁——临界区为纯内存算术（百纳秒级），
// 2000 QPS 下锁竞争可忽略；分桶锁的复杂度在当前量级属过度设计。
func (l *MemoryLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		// 首次访问：满桶起步（新用户获得完整突发额度）
		l.buckets[key] = &memoryBucket{tokens: l.capacity, lastTime: now}
		return true
	}
	// 惰性补充：按时间差补令牌，封顶容量
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}
	b.lastTime = now
	// 消耗判定：有令牌即放行并扣减
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
