package repository

import (
	"ChatServer/model"
	"context"

	"gorm.io/gorm"
)

// UserRepository 用户数据访问层
// 职责：只负责 GORM 的 CRUD 操作，不含业务逻辑
// 设计原则：
//   - 返回数据库原始错误（如 gorm.ErrRecordNotFound）
//   - 不进行业务判断（如密码校验）
//   - 不进行错误转换（错误转换在 Service 层完成）
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository 创建用户仓储实例
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// GetByPhone 根据手机号查询用户信息
// 返回：用户模型和数据库原始错误
// 场景：用户登录、注册前检查手机号是否已存在
func (r *UserRepository) GetByPhone(ctx context.Context, telephone string) (*model.UserInfo, error) {
	var user model.UserInfo
	err := r.db.WithContext(ctx).Where("telephone = ?", telephone).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByUUID 根据 UUID 查询用户信息
// 返回：用户模型和数据库原始错误
// 场景：刷新 Token、查询用户详情
func (r *UserRepository) GetByUUID(ctx context.Context, uuid string) (*model.UserInfo, error) {
	var user model.UserInfo
	err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// Create 创建新用户
// 返回：创建的用户模型和数据库原始错误
// 场景：用户注册
func (r *UserRepository) Create(ctx context.Context, user *model.UserInfo) (*model.UserInfo, error) {
	err := r.db.WithContext(ctx).Create(user).Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

// Update 更新用户信息
// 返回：更新后的用户模型和数据库原始错误
// 场景：修改密码、更新用户资料
func (r *UserRepository) Update(ctx context.Context, user *model.UserInfo) (*model.UserInfo, error) {
	err := r.db.WithContext(ctx).Save(user).Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ExistsByPhone 检查手机号是否已存在
// 返回：是否存在和数据库原始错误
// 场景：注册时手机号唯一性校验
func (r *UserRepository) ExistsByPhone(ctx context.Context, telephone string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserInfo{}).Where("telephone = ?", telephone).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateLastLogin 更新最后登录时间
// 返回：数据库原始错误
// 场景：用户登录成功后更新登录时间
func (r *UserRepository) UpdateLastLogin(ctx context.Context, userUUID string) error {
	return r.db.WithContext(ctx).Model(&model.UserInfo{}).Where("uuid = ?", userUUID).Update("updated_at", gorm.Expr("NOW()")).Error
}
