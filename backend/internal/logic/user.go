// Package logic 业务逻辑层：编排 DAO 与工具函数，实现业务规则（密码强度、查重、哈希、签发 token）。
// 分层约束：不碰 HTTP（不 import gin），不写 SQL（数据操作全走 dao），业务错误用哨兵 error 上抛给 controller 翻译。
package logic

import (
	"campuscommunity/internal/dao"
	"campuscommunity/internal/model"
	"campuscommunity/pkg/utils/jwt"
	"campuscommunity/pkg/utils/password"
	"campuscommunity/pkg/utils/snowflake"
	"errors"
	"fmt"
	"unicode"

	"github.com/go-sql-driver/mysql"
)

// 用户模块哨兵错误：controller 用 errors.Is 识别并映射为业务响应码。
// 不直接返回 ResCode——logic 层对 HTTP 响应码无感知，保证分层干净、便于单测。
var (
	ErrUserExist    = errors.New("用户名已存在")
	ErrWeakPassword = errors.New("密码强度不足")
	ErrWrongLogin   = errors.New("用户名或密码错误")
	ErrUserNotFound = errors.New("用户不存在")
)

// SignUp 注册：密码强度校验 → 用户名查重 → bcrypt 哈希 → 雪花 ID → 落库。
func SignUp(p *model.ParamRegister) error {
	// 1. 密码强度校验（mvp §1.4：长度≥8 且含字母+数字，业务规则在 logic 层实现）
	if !isStrongPassword(p.Password) {
		return ErrWeakPassword
	}
	// 2. 前置查重：快速失败，省一次 bcrypt 计算（约 60ms）。
	//    注意存在竞态窗口：两个请求同时通过查重 → 靠 DB 唯一索引兜底（见步骤 5）
	exist, err := dao.GetUserByUsername(p.Username)
	if err != nil {
		return fmt.Errorf("logic: signup check username: %w", err)
	}
	if exist != nil {
		return ErrUserExist
	}
	// 3. 密码哈希：bcrypt 自动加盐，同一明文每次哈希结果不同
	hash, err := password.Hash(p.Password)
	if err != nil {
		return fmt.Errorf("logic: signup hash password: %w", err)
	}
	// 4. 组装用户：user_id 用雪花算法生成（对外暴露的业务主键），
	//    昵称默认取用户名，避免新用户在列表页展示为空
	user := &model.User{
		UserID:   snowflake.GenID(),
		Username: p.Username,
		Password: hash,
		Nickname: p.Username,
	}
	// 5. 落库：并发注册同名用户时唯一索引冲突（errno 1062）-> 翻译为业务错误而非 500
	//    特别注意此处当高并发场景时会出现重复用户名，所以必须使用唯一索引来解决冲突
	if err := dao.CreateUser(user); err != nil {
		if isDuplicateEntryErr(err) {
			return ErrUserExist
		}
		return fmt.Errorf("logic: signup create user: %w", err)
	}
	return nil
}

// Login 登录：查用户 → 比对密码 → 签发 JWT（7 天，见 conf）。
// 返回 token 字符串，用户信息由前端通过 GET /user/profile 获取，登录接口保持单一职责。
func Login(p *model.ParamLogin) (string, error) {
	// 1. 按用户名查用户（不存在返回 nil, nil）
	user, err := dao.GetUserByUsername(p.Username)
	if err != nil {
		return "", fmt.Errorf("logic: login get user: %w", err)
	}
	// 2. 用户不存在与密码错误统一返回同一错误：避免泄露「该用户名是否已注册」（防撞库）
	if user == nil {
		return "", ErrWrongLogin
	}
	// 3. bcrypt 比对（内部常量时间比较，防时序攻击）
	if err := password.Check(p.Password, user.Password); err != nil {
		return "", ErrWrongLogin
	}
	// 4. 签发 JWT：payload 仅含 user_id + exp + iss（mvp §1.4）
	token, err := jwt.GenToken(user.UserID)
	if err != nil {
		return "", fmt.Errorf("logic: login gen token: %w", err)
	}
	return token, nil
}

// GetUserProfile 查看个人资料（当前登录用户）。
// 正常流程 token 有效则用户必存在；user == nil 属于理论边界（如手动删库），按业务错误处理。
func GetUserProfile(userID int64) (*model.User, error) {
	user, err := dao.GetUserByUserID(userID)
	if err != nil {
		return nil, fmt.Errorf("logic: get profile: %w", err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return user, nil
}

// UpdateUserProfile 修改个人资料（PATCH 部分更新：只更新前端提交的字段）。
// 指针判空：*string 为 nil 表示 本次未提交该字段-> 不更新；
// 非 nil（即使指向空串）表示前端明确提交了该值-> 落库，空串即清空该字段。
// map 组装放在 logic 层、DAO 只负责执行——哪些字段要更新是业务意图，DAO 保持薄。
func UpdateUserProfile(userID int64, p *model.ParamUpdateProfile) error {
	// 更新前确认用户存在：JWT 有效期内用户被删（理论场景）时给出明确业务错误，
	// 而不是 Updates 静默影响 0 行让用户以为改成功了
	user, err := dao.GetUserByUserID(userID)
	if err != nil {
		return fmt.Errorf("logic: update profile check user: %w", err)
	}
	if user == nil {
		return ErrUserNotFound
	}
	// 动态构建更新 map：nil 跳过（不更新），非 nil 写入（空串 = 清空）
	data := make(map[string]any)
	if p.Nickname != nil {
		data["nickname"] = *p.Nickname
	}
	if p.Phone != nil {
		data["phone"] = *p.Phone
	}
	if p.Avatar != nil {
		data["avatar"] = *p.Avatar
	}
	// 全部字段都未提交（空 body `{}`）→ 无任何更新，幂等返回成功
	if len(data) == 0 {
		return nil
	}
	if err := dao.UpdateUserProfile(userID, data); err != nil {
		return fmt.Errorf("logic: update profile: %w", err)
	}
	return nil
}

// UpdateUserAddress 修改收货地址（单字段，后续下单时快照到 orders.address）。
func UpdateUserAddress(userID int64, p *model.ParamUpdateAddress) error {
	// 同上：先确认用户存在
	user, err := dao.GetUserByUserID(userID)
	if err != nil {
		return fmt.Errorf("logic: update address check user: %w", err)
	}
	if user == nil {
		return ErrUserNotFound
	}
	if err := dao.UpdateUserAddress(userID, p.Address); err != nil {
		return fmt.Errorf("logic: update address: %w", err)
	}
	return nil
}

// isStrongPassword 密码强度规则（mvp §1.4）：长度 ≥ 8 且同时含字母和数字。
// 不用正则：单次遍历即可统计，无额外依赖、可读性更好。
func isStrongPassword(pwd string) bool {
	if len(pwd) < 8 {
		return false
	}
	hasLetter, hasDigit := false, false
	for _, c := range pwd {
		switch {
		case unicode.IsLetter(c):
			hasLetter = true
		case unicode.IsDigit(c):
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

// isDuplicateEntryErr 判断是否 MySQL 唯一索引冲突（errno 1062）。
// 注册前置查重存在竞态窗口，唯一索引是数据一致性的最终防线；
// 冲突时应翻译为「用户名已存在」业务错误，而非向上抛 500。
func isDuplicateEntryErr(err error) bool {
	var me *mysql.MySQLError
	// errors.As 沿错误链查找（fmt.Errorf %w 包装后仍可识别）
	// errors.As 递归遍历错误链，自动解包找到原始的MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
