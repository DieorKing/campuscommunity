package snowflake

import (
	"time"

	sf "github.com/bwmarrin/snowflake"
)

var node *sf.Node

// Init 初始化雪花 ID 生成器。
// startTime 为起始纪元，格式 "2006-01-02"（Go 参考时间）。
// datacenterID / workerID 范围 0-31，内部组合为 10 位机器位：machineID = datacenterID<<5 | workerID。
// 初始化失败时返回 err，调用方应 log.Fatal 退出，不可降级。
func Init(startTime string, datacenterID, workerID int64) error {
	st, err := time.Parse("2006-01-02", startTime)
	if err != nil {
		return err
	}
	sf.Epoch = st.UnixNano() / 1000000
	node, err = sf.NewNode(datacenterID<<5 | workerID)
	return err
}

// GenID 生成雪花 ID（int64）。
// 若未调用 Init 初始化，显式 panic 提示，避免 nil 指针误用难排查。
func GenID() int64 {
	if node == nil {
		panic("snowflake: not initialized, call Init first")
	}
	return node.Generate().Int64()
}
