package model

import (
	"strconv"
)

// ID 雪花业务主键的 JSON 传输类型（int64 的语义化别名）。
// 为什么存在：雪花 ID 是 17~19 位 int64，而 JS Number 是 IEEE754 双精度
// 浮点，安全整数上限 2^53-1（16 位）——以 JSON 数字下发会被前端静默
// 改写（实测 83133369331879936 → 83133369331879940），详情页查无此 ID。
// 业界统一解法（Twitter/微信开放平台同款）：对外序列化为字符串。
//
// 用法：响应 DTO 的 ID 字段声明为 ID 类型（数据库列仍为 BIGINT，GORM
// 按 int64 底层扫描/写入，无需改表）；前端全链路按字符串处理 ID。
type ID int64

// String 实现 Stringer（fmt 打印、URL 拼接走此方法，输出精确十进制）。
func (i ID) String() string {
	return strconv.FormatInt(int64(i), 10)
}

// MarshalJSON 序列化为 JSON 字符串："83133369331879936"。
// 前端拿到字符串，无精度丢失；需要数字的旧调用方可直接 JSON.parse 后
// 按 string 使用。
func (i ID) MarshalJSON() ([]byte, error) {
	return strconv.AppendQuote(nil, i.String()), nil
}

// Int64 显式转 int64（DAO/内部计算用，类型不隐式转换是 Go 的保护）。
func (i ID) Int64() int64 {
	return int64(i)
}
