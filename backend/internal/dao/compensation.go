// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 本文件为补偿模块的 MySQL 数据访问：失败现场落任务 + 扫描器捞取 + 终态流转。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// CreateCompensationTask 失败现场落任务记录（INSERT，status=pending 初态）。
// 调用点：logic 层各 best-effort 失败分支（热榜/延时关单/通知失败时）。
// 落任务本身失败：只能降级为纯日志——补偿动作自身失败无法再被补偿
// （无限递归保证不可行），最外层以日志+人工兜底。
func CreateCompensationTask(t *model.CompensationTask) error {
	if err := mysql.GetDB().Create(t).Error; err != nil {
		return fmt.Errorf("dao: create compensation task: %w", err)
	}
	return nil
}

// GetDueCompensationTasks 捞到期任务（扫描器每轮调用）。
// 这里不走 DB 的 next_retry_at 索引查询——调度真值源是 Redis ZSet
// （按 score 精确到期唤醒，O(logN)）；本函数只在 ZSet 给出到期 task_id
// 后回表取行。入参 ids 来自 ZSet 捞取（有界：每轮到期任务数量级小）。
// 返回行不含已终态/已删行（ZSet 残留导致的捞空，正常容忍）。
func GetDueCompensationTasks(ids []int64) ([]model.CompensationTask, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.CompensationTask
	// 状态过滤 pending：failed 终态行可能 ZSet 残留（终态翻转后 ZREM
	// best-effort 失败），这里二次防御不捞
	if err := mysql.GetDB().
		Where("task_id IN ? AND status = ?", ids, model.CompPending).
		Find(&list).Error; err != nil {
		return nil, fmt.Errorf("dao: get due compensation tasks: %w", err)
	}
	return list, nil
}

// SucceedCompensationTask 补偿成功：删行。
// 删行而非标记 succeeded：任务表是「待处理清单」不是审计日志——
// 成功的补偿无需保留（成功事实已在业务数据里），删行防表膨胀；
// 审计需求由 binlog/日志承担，不占业务表。
// 删行 + ZREM 由调用方（补偿消费者）配合完成，两者都幂等可重放。
func SucceedCompensationTask(taskID int64) error {
	// 物理删除：表无软删字段；残留重放（行已删）rows=0 无害幂等
	r := mysql.GetDB().
		Where("task_id = ?", taskID).
		Delete(&model.CompensationTask{})
	if r.Error != nil {
		return fmt.Errorf("dao: succeed compensation task: %w", r.Error)
	}
	return nil
}

// FailCompensationTask 补偿重试失败：退避再入队或翻终态。
// 两个分支（调用方 retry 计数已判定）：
//
//	retry < MaxRetry：retry_count+1 + next_retry_at=now+退避 → 行保留 pending
//	retry = MaxRetry：翻 failed 终态（人工工单入口，不再调度）
//
// 用 map Updates 单语句原子更新（读改写拆两条会竞态：两个扫描器实例
// 并发处理同一任务时 retry_count 会丢更新）。
// 返回 (retryCount, terminal, err)：terminal=true 表示已翻终态
// （调用方据此跳过重新 ZADD）。
func FailCompensationTask(taskID int64, lastErr string) (retryCount int, terminal bool, err error) {
	now := time.Now()
	var t model.CompensationTask
	// 先取行拿当前 retry_count：更新值依赖旧行（退避延迟按次数算）。
	// 取行与更新的间隙竞态由 Updates 的 WHERE status=pending 守卫：
	// 行已被并发者翻终态/删除则 rows=0，本调用视为已处理（幂等落空）
	if err := mysql.GetDB().Where("task_id = ?", taskID).First(&t).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, false, nil // 行已删（并发者成功）：无欠可欠，静默
		}
		return 0, false, fmt.Errorf("dao: fail compensation task get: %w", err)
	}
	newRetry := t.RetryCount + 1
	// 达到上限：翻 failed 终态（最后一试也失败，留人工）
	if newRetry >= model.CompMaxRetry {
		r := mysql.GetDB().Model(&model.CompensationTask{}).
			Where("task_id = ? AND status = ?", taskID, model.CompPending).
			Updates(map[string]any{
				"status":        model.CompFailed,
				"retry_count":   newRetry,
				"last_error":    lastErr,
				"next_retry_at": now,
			})
		if r.Error != nil {
			return 0, false, fmt.Errorf("dao: fail compensation task terminal: %w", r.Error)
		}
		return newRetry, r.RowsAffected == 1, nil
	}
	// 未达上限：退避重入队（行保留 pending，retry+1，下次时间=now+退避）
	r := mysql.GetDB().Model(&model.CompensationTask{}).
		Where("task_id = ? AND status = ?", taskID, model.CompPending).
		Updates(map[string]any{
			"retry_count":   newRetry,
			"last_error":    lastErr,
			"next_retry_at": now.Add(model.BackoffDelay(newRetry)),
		})
	if r.Error != nil {
		return 0, false, fmt.Errorf("dao: fail compensation task backoff: %w", r.Error)
	}
	return newRetry, false, nil
}
