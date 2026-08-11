package password

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Cost 是 bcrypt 的计算成本因子，值越大越安全但越慢。
// grill 固化决策：cost=10（[mvp §1.4](../../../docs/mvp-v1.0-features.md)），
// 10 是业界默认值，单次哈希约 60ms，兼顾安全与性能。
const Cost = 10

// Hash 将明文密码加密为 bcrypt 哈希字符串。
// 用于用户注册、修改密码时落库前调用。
// 返回的哈希串长度固定 60 字符，可直接存入 VARCHAR(100) 字段。
func Hash(password string) (string, error) {
	// bcrypt.GenerateFromPassword 内部会自动生成随机盐并嵌入哈希结果，
	// 因此同一明文每次哈希结果不同，无需单独存盐字段。
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), Cost)
	if err != nil {
		// cost 超出 bcrypt 限制（MaxCost=31）才会出错，Cost=10 不会触发
		return "", fmt.Errorf("bcrypt hash failed,err: %w", err)
	}
	return string(bytes), nil
}

// Check 校验明文密码是否与存储的 bcrypt 哈希匹配。
// 用于用户登录时比对密码。
// 匹配返回 nil，不匹配返回错误（不区分"密码错"和"哈希格式错"，避免信息泄露）。
func Check(password, hash string) error {
	// bcrypt.CompareHashAndPassword 内部从 hash 中提取盐和 cost 重新计算，
	// 与传入 hash 常量时间比对，防止时序攻击。
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
