package repository

import (
	"ChatServer/model"
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// authRepositoryImpl 认证相关数据访问层实现
type authRepositoryImpl struct {
	db          *gorm.DB
	redisClient *redis.Client
}

// NewAuthRepository 创建认证仓储实例
func NewAuthRepository(db *gorm.DB, redisClient *redis.Client) IAuthRepository {
	return &authRepositoryImpl{db: db, redisClient: redisClient}
}

// GetByPhone 根据手机号查询用户信息
func (r *authRepositoryImpl) GetByPhone(ctx context.Context, telephone string) (*model.UserInfo, error) {
	return nil, nil // TODO: 根据手机号查询用户信息
}

// GetByEmail 根据邮箱查询用户信息
func (r *authRepositoryImpl) GetByEmail(ctx context.Context, email string) (*model.UserInfo, error) {
	var user model.UserInfo
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		return nil, WrapDBError(err)
	}
	return &user, nil
}

// VerifyVerifyCode 校验验证码
func (r *authRepositoryImpl) VerifyVerifyCode(ctx context.Context, email, verifyCode string) (bool, error) {
	// 从Redis中获取验证码
	verifyCodeKey := fmt.Sprintf("user:verify_code:%s", email)
	verifyCodeValue, err := r.redisClient.Get(ctx, verifyCodeKey).Result()
	if err != nil {
		return false, WrapRedisError(err)
	}
	return verifyCodeValue == verifyCode, nil
}

// ExistsByPhone 检查手机号是否已存在
func (r *authRepositoryImpl) ExistsByPhone(ctx context.Context, telephone string) (bool, error) {
	return false, nil // TODO: 检查手机号是否已存在
}

// ExistsByEmail 检查邮箱是否已存在
func (r *authRepositoryImpl) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return false, nil // TODO: 检查邮箱是否已存在
}

// Create 创建新用户
func (r *authRepositoryImpl) Create(ctx context.Context, user *model.UserInfo) (*model.UserInfo, error) {
	err := r.db.WithContext(ctx).Create(user).Error
	if err != nil {
		return nil, WrapDBError(err)
	}
	return user, nil
}

// UpdateLastLogin 更新最后登录时间
func (r *authRepositoryImpl) UpdateLastLogin(ctx context.Context, userUUID string) error {
	return nil // TODO: 更新最后登录时间
}

// UpdatePassword 更新密码
func (r *authRepositoryImpl) UpdatePassword(ctx context.Context, userUUID, password string) error {
	return nil // TODO: 更新密码
}
