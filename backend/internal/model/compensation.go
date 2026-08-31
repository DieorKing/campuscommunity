package model

import "time"

// ── 单据地图（补偿模块·表）───────────────────────────────
// CompensationTask  → compensation_tasks 表行：某旁路动作失败的记录，
//                    待补偿系统按退避节奏重试
// CompensationType  → 补偿任务类型枚举（热榜重算/延时关单重放/通知重发）
// ─────────────────────────────────────────────────────────

// CompensationType 补偿任务类型：决定消费者执行哪个补偿动作。
// 任务表是多态的（一张表装多种任务），type 字段分流处理逻辑。
type CompensationType string

const (
	// CompHotRank 热榜重算型：payload={good_id}，执行 COUNT(orders)+ZADD 覆盖。
	// 为什么不是 ZINCRBY 重放：+1 是相对操作，超时失败时服务器可能已执行，
	// 盲重试双重计数——绝对操作（=COUNT 结果）重放 N 次天然收敛
	CompHotRank CompensationType = "hot_rank"
	// CompOrderClose 延时关单重放型：payload={order_id}，执行原样 ZADD。
	// ZADD 对已存在 member 是覆盖语义（重置到期时间），重放幂等
	CompOrderClose CompensationType = "order_close"
	// CompNotification 通知重发型：payload=完整 NotificationMessage JSON。
	// 文案不可重算（人写的不是查出来的），重放要带全套内容；
	// 消费端 (user, category, ref_id) 唯一索引兜底重复投递
	CompNotification CompensationType = "notification"
)

// 补偿任务状态机：pending（待重试）→ succeeded（补偿成功，行删除或标记）
//
//	↘ failed（≥5 次仍失败，终态留人工）
//
// 与 MQ 消费侧「确定性失败 ack / 暂时性失败 nack」同构：补偿重试只有
// 暂时性失败（DB/Redis 抖动）会继续退避；动作本身的问题（毒任务）
// 由 retry_count 封顶兜住——不设重试分类是因为补偿动作已保证幂等，
// 重试无害，唯一要防的是「永远修不好」的死循环。
type CompensationStatus string

const (
	CompPending   CompensationStatus = "pending"   // 待重试（初态）
	CompSucceeded CompensationStatus = "succeeded" // 补偿成功（终态）
	CompFailed    CompensationStatus = "failed"    // 重试耗尽转终态（人工工单入口）
)

// 重试节奏常量：指数退避 1s/2s/4s/8s/16s，5 次封顶转 failed。
// 为什么 5 次：基础设施抖动的恢复时间量级是秒~分钟，1+2+4+8+16=31s
// 覆盖一次典型抖动窗口；连续 5 次失败说明不是抖动是故障/bug，
// 继续重试只是浪费——封顶转人工（三层兜底的第三层）。
const (
	CompMaxRetry = 5
)

// backoffDelay 第 retry 次失败后的下次重试延迟（指数退避）。
// retry 从 1 数起（第 1 次失败 → 等 1s；第 5 次失败 → 16s 后最后一试）。
func BackoffDelay(retry int) time.Duration {
	// 1<<(retry-1)：retry=1→1s, 2→2s, 3→4s, 4→8s, 5→16s
	return time.Duration(1<<(retry-1)) * time.Second
}

// CompensationTask compensation_tasks 表行：某旁路动作失败的事实记录。
// 事实源在 DB（行在=未完成），调度在 Redis（delay:compensation:retry ZSet
// 按 score=下次重试时间精确唤醒）——沿用延时关单的「DB 真值源 + ZSet 调度」选型。
// 生命周期：失败现场 INSERT(pending) → 扫描器捞到期任务 → 执行
//
//	成功 → 删行
//	失败且 retry<5 → retry_count+1 + 重新 ZADD（更晚的 score）
//	失败且 retry=5 → 翻 failed 终态（保留行做人工排查，不再调度）
type CompensationTask struct {
	BaseModel
	TaskID ID `gorm:"uniqueIndex;not null;comment:业务主键(雪花)" json:"task_id"`
	// Type 多态分流：消费者按 type 执行对应补偿动作（同 RefID 多态哲学）
	Type CompensationType `gorm:"type:varchar(30);index;not null;comment:任务类型" json:"type"`
	// Payload 补偿动作的参数。设计判据「可重算带 id，不可重算带数据」：
	// hot_rank 只带 good_id（COUNT 可重算）/ order_close 只带 order_id /
	// notification 带完整消息 JSON（文案不可重算）。
	// 用 string 不用 JSON 类型：MySQL 5.7 JSON 函数用不上，varchar 够用且
	// 消费端自己 Unmarshal——payload 结构归各 type 私有，表结构不感知
	Payload string `gorm:"type:varchar(1000);not null;comment:任务参数JSON" json:"payload"`
	// RetryCount 已重试次数（0=刚落，5=最后一试失败将翻终态）
	RetryCount int                `gorm:"default:0;comment:已重试次数" json:"retry_count"`
	Status     CompensationStatus `gorm:"type:varchar(20);index;not null;default:pending;comment:状态" json:"status"`
	// LastError 最后一次失败原因（排查用；每次重试覆盖，只留最近一次）
	LastError string `gorm:"type:varchar(500);comment:最后失败原因" json:"last_error"`
	// NextRetryAt 下次重试时间（DB 冗余列：ZSet score 的镜像，
	// 供人工排查/全表对账用；调度仍以 ZSet 为准）
	NextRetryAt time.Time `gorm:"comment:下次重试时间" json:"next_retry_at"`
}
