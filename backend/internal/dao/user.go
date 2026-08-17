// Package dao 数据访问层：封装所有业务表的 CRUD，不含业务逻辑。
// 上层（logic）只调用本层函数，不直接触碰 GORM API，保证数据访问可集中优化与替换。
package dao

import (
	"campuscommunity/internal/dao/mysql"
	"campuscommunity/internal/model"
	"fmt"

	"gorm.io/gorm"
)

// CreateUser 新增用户（注册时调用）。
// username / user_id 均有唯一索引，重复插入会返回错误，
// 由 logic 层识别并翻译为「用户名已存在」业务响应码（DAO 不做业务判断）。
func CreateUser(user *model.User) error {
	if err := mysql.GetDB().Create(user).Error; err != nil {
		return fmt.Errorf("dao: create user: %w", err)
	}
	return nil
}

// GetUserByUsername 按用户名查询（登录验证、注册查重共用）。
// 返回约定：用户不存在返回 (nil, nil)——「不存在」是正常业务分支而非错误，
// 调用方用 user == nil 判断；仅 DB 真正出错才返回 err。
// 这样 logic 层无需 import gorm 判断 ErrRecordNotFound，分层更干净。
func GetUserByUsername(username string) (*model.User, error) {
	var user model.User
	err := mysql.GetDB().Where("username = ?", username).First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get user by username: %w", err)
	}
	return &user, nil
}

// GetUserByUserID 按业务主键 user_id 查询（查个人资料、资源所有者校验时用）。
// 返回约定同上：不存在返回 (nil, nil)。
func GetUserByUserID(userID int64) (*model.User, error) {
	var user model.User
	err := mysql.GetDB().Where("user_id = ?", userID).First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dao: get user by user_id: %w", err)
	}
	return &user, nil
}

// UpdateUserProfile 更新个人资料字段（PATCH 部分更新，配合 logic 层动态组装）。
// data 为 map[string]any，由 logic 层按「前端提交了哪些字段」组装后传入：
// GORM Updates(map) 只更新 map 中出现的列，其余列保持不动，天然支持部分更新。
// 语义：未出现在 map 中的字段 = 本次不修改；出现在 map 中且值为空串 = 清空该字段。
// 用 map 而非 struct 传参：map 能表达「空串=清空」，struct 的零值会被 GORM 跳过，语义不直观。
func UpdateUserProfile(userID int64, data map[string]any) error {
	result := mysql.GetDB().Model(&model.User{}).
		Where("user_id = ?", userID).
		Updates(data)
	if result.Error != nil {
		return fmt.Errorf("dao: update user profile: %w", result.Error)
	}
	// RowsAffected == 0 意味着 userID 不存在——logic 层调用前通常已查过用户，
	// 此处不重复判空（防御性校验在 logic 做，DAO 保持薄）。
	return nil
}

// UpdateUserAddress 更新收货地址（单字段，mvp §1.4）。
// 独立于 UpdateUserProfile：改地址是独立接口（PUT /user/address），
// 单独的函数让每次更新只动一列，意图清晰且不会误碰资料字段。
func UpdateUserAddress(userID int64, address string) error {
	result := mysql.GetDB().Model(&model.User{}).
		Where("user_id = ?", userID).
		Update("address", address)
	if result.Error != nil {
		return fmt.Errorf("dao: update user address: %w", result.Error)
	}
	return nil
}
