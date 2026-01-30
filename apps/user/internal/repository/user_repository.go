package repository

import (
	"ChatServer/apps/user/mq"
	"ChatServer/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// userRepositoryImpl 用户信息数据访问层实现
type userRepositoryImpl struct {
	db          *gorm.DB
	redisClient *redis.Client
}

// NewUserRepository 创建用户信息仓储实例
func NewUserRepository(db *gorm.DB, redisClient *redis.Client) IUserRepository {
	return &userRepositoryImpl{db: db, redisClient: redisClient}
}

// GetByUUID 根据UUID查询用户信息
func (r *userRepositoryImpl) GetByUUID(ctx context.Context, uuid string) (*model.UserInfo, error) {
	// ==================== 1. 先从 Redis 缓存中查询 ====================
	cacheKey := fmt.Sprintf("user:info:%s", uuid)
	cachedData, err := r.redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		// 缓存命中，反序列化返回
		// 先判空
		if cachedData == "{}" {
			return nil, nil
		}
		var user model.UserInfo
		if err := json.Unmarshal([]byte(cachedData), &user); err == nil {
			return &user, nil
		}
	}
	if err != nil && err != redis.Nil {
		LogRedisError(ctx, err) // 记录日志 降级处理
	}

	// ==================== 2. 缓存未命中，查询 MySQL ====================
	var user model.UserInfo
	err = r.db.WithContext(ctx).Where("uuid = ? AND deleted_at IS NULL", uuid).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 存一份空到redis 5min过期
			randomDuration := getRandomExpireTime(5 * time.Minute)
			err = r.redisClient.Set(ctx, cacheKey, "{}", randomDuration).Err()
			if err != nil {
				// 发送到重试队列
				task := mq.BuildSetTask(cacheKey, "{}", randomDuration).
					WithSource("UserRepository.GetByUUID.EmptyCache")
				LogAndRetryRedisError(ctx, task, err)
			}
			return nil, nil
		} else {
			return nil, WrapDBError(err)
		}
	}

	// ==================== 3. 存入 Redis 缓存 ====================
	// 序列化用户信息
	userJSON, err := json.Marshal(user)
	if err != nil {
		// 序列化失败，不影响主流程，只返回数据库数据
		return &user, nil
	}

	// 存入缓存，设置过期时间为 1 小时（+-5min缓冲）
	// 随机时间防止缓存雪崩
	randomDuration := time.Duration(rand.Intn(10)) * time.Minute
	ttl := 1*time.Hour - randomDuration
	err = r.redisClient.Set(ctx, cacheKey, userJSON, ttl).Err()
	if err != nil {
		// 发送到重试队列，不影响主流程
		task := mq.BuildSetTask(cacheKey, string(userJSON), ttl).
			WithSource("UserRepository.GetByUUID.SetCache")
		LogAndRetryRedisError(ctx, task, err)
		return &user, nil
	}

	return &user, nil
}

// GetByPhone 根据手机号查询用户信息
func (r *userRepositoryImpl) GetByPhone(ctx context.Context, telephone string) (*model.UserInfo, error) {
	return nil, nil // TODO: 实现查询用户信息
}

// BatchGetByUUIDs 批量查询用户信息
func (r *userRepositoryImpl) BatchGetByUUIDs(ctx context.Context, uuids []string) ([]*model.UserInfo, error) {
	if len(uuids) == 0 {
		return []*model.UserInfo{}, nil
	}

	// 查询数据库
	var users []*model.UserInfo
	err := r.db.WithContext(ctx).
		Where("uuid IN ? AND deleted_at IS NULL", uuids).
		Find(&users).
		Error
	if err != nil {
		return nil, WrapDBError(err)
	}

	return users, nil
}

// Update 更新用户信息
func (r *userRepositoryImpl) Update(ctx context.Context, user *model.UserInfo) (*model.UserInfo, error) {
	return nil, nil // TODO: 实现更新用户信息
}

// UpdateAvatar 更新用户头像
func (r *userRepositoryImpl) UpdateAvatar(ctx context.Context, userUUID, avatar string) error {
	// 更新头像到数据库
	err := r.db.WithContext(ctx).
		Model(&model.UserInfo{}).
		Where("uuid = ? AND deleted_at IS NULL", userUUID).
		Update("avatar", avatar).
		Error
	if err != nil {
		return WrapDBError(err)
	}

	// 更新成功后，删除 Redis 缓存
	cacheKey := fmt.Sprintf("user:info:%s", userUUID)
	err = r.redisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildDelTask(cacheKey).
			WithSource("UserRepository.UpdateAvatar")
		LogAndRetryRedisError(ctx, task, err)
	}

	return nil
}

// UpdateBasicInfo 更新基本信息
func (r *userRepositoryImpl) UpdateBasicInfo(ctx context.Context, userUUID string, nickname, signature, birthday string, gender int8) error {
	// 构造更新字段
	updates := map[string]interface{}{
		"updated_at": time.Now(),
	}

	if nickname != "" {
		updates["nickname"] = nickname
	}
	if signature != "" {
		updates["signature"] = signature
	}
	if birthday != "" {
		updates["birthday"] = birthday
	}
	if gender > 0 {
		updates["gender"] = gender
	}

	// 执行更新
	err := r.db.WithContext(ctx).
		Model(&model.UserInfo{}).
		Where("uuid = ? AND deleted_at IS NULL", userUUID).
		Updates(updates).
		Error
	if err != nil {
		return WrapDBError(err)
	}

	// 更新成功后，删除Redis缓存
	cacheKey := fmt.Sprintf("user:info:%s", userUUID)
	err = r.redisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildDelTask(cacheKey).
			WithSource("UserRepository.UpdateNickname")
		LogAndRetryRedisError(ctx, task, err)
	}

	return nil
}

// UpdateEmail 更新邮箱
func (r *userRepositoryImpl) UpdateEmail(ctx context.Context, userUUID, email string) error {
	// 更新邮箱到数据库
	err := r.db.WithContext(ctx).
		Model(&model.UserInfo{}).
		Where("uuid = ? AND deleted_at IS NULL", userUUID).
		Update("email", email).
		Error
	if err != nil {
		return WrapDBError(err)
	}

	// 更新成功后，删除Redis缓存
	cacheKey := fmt.Sprintf("user:info:%s", userUUID)
	err = r.redisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildDelTask(cacheKey).
			WithSource("UserRepository.UpdateEmail")
		LogAndRetryRedisError(ctx, task, err)
	}

	return nil
}

// UpdateTelephone 更新手机号
func (r *userRepositoryImpl) UpdateTelephone(ctx context.Context, userUUID, telephone string) error {
	return nil // TODO: 实现更新手机号
}

// Delete 软删除用户（注销账号）
// 设置 deleted_at 字段，删除 Redis 缓存
func (r *userRepositoryImpl) Delete(ctx context.Context, userUUID string) error {
	// 1. 软删除用户（GORM 会自动设置 deleted_at 时间戳）
	err := r.db.WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", userUUID).
		Delete(&model.UserInfo{}).
		Error
	if err != nil {
		return WrapDBError(err)
	}

	// 2. 删除 Redis 缓存
	cacheKey := fmt.Sprintf("user:info:%s", userUUID)
	err = r.redisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildDelTask(cacheKey).
			WithSource("UserRepository.Delete")
		LogAndRetryRedisError(ctx, task, err)
	}

	return nil
}

// ExistsByPhone 检查手机号是否已存在
func (r *userRepositoryImpl) ExistsByPhone(ctx context.Context, telephone string) (bool, error) {
	return false, nil // TODO: 实现检查手机号是否已存在
}

// ExistsByEmail 检查邮箱是否已存在
func (r *userRepositoryImpl) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.UserInfo{}).
		Where("email = ? AND deleted_at IS NULL", email).
		Count(&count).
		Error
	if err != nil {
		return false, WrapDBError(err)
	}
	return count > 0, nil
}

// UpdatePassword 更新密码
func (r *userRepositoryImpl) UpdatePassword(ctx context.Context, userUUID, password string) error {
	// 更新密码到数据库
	err := r.db.WithContext(ctx).
		Model(&model.UserInfo{}).
		Where("uuid = ? AND deleted_at IS NULL", userUUID).
		Update("password", password).
		Error
	if err != nil {
		return WrapDBError(err)
	}

	// 更新成功后，删除Redis缓存
	cacheKey := fmt.Sprintf("user:info:%s", userUUID)
	err = r.redisClient.Del(ctx, cacheKey).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildDelTask(cacheKey).
			WithSource("UserRepository.UpdatePassword")
		LogAndRetryRedisError(ctx, task, err)
	}

	return nil
}

// SaveQRCode 保存用户二维码
// 将二维码 token 与用户 UUID 的映射关系存储到 Redis
// 同时保存反向映射: user:qrcode:user:{userUUID} -> token
// 过期时间: 48小时
func (r *userRepositoryImpl) SaveQRCode(ctx context.Context, userUUID, token string) error {
	// 1. 保存 token -> userUUID 映射
	tokenKey := fmt.Sprintf("user:qrcode:token:%s", token)
	err := r.redisClient.Set(ctx, tokenKey, userUUID, 48*time.Hour).Err()
	if err != nil {
		return WrapRedisError(err)
	}

	// 2. 保存 userUUID -> token 反向映射
	userKey := fmt.Sprintf("user:qrcode:user:%s", userUUID)
	err = r.redisClient.Set(ctx, userKey, token, 48*time.Hour).Err()
	if err != nil {
		return WrapRedisError(err)
	}

	return nil
}

// GetUUIDByQRCodeToken 根据 token 获取用户 UUID
func (r *userRepositoryImpl) GetUUIDByQRCodeToken(ctx context.Context, token string) (string, error) {
	tokenKey := fmt.Sprintf("user:qrcode:token:%s", token)
	userUUID, err := r.redisClient.Get(ctx, tokenKey).Result()
	if err != nil {
		if err == redis.Nil {
			return "", ErrRedisNil
		}
		return "", WrapRedisError(err)
	}
	return userUUID, nil
}

// GetQRCodeTokenByUserUUID 根据用户 UUID 获取二维码 token和剩余时间
func (r *userRepositoryImpl) GetQRCodeTokenByUserUUID(ctx context.Context, userUUID string) (string, time.Time, error) {
	userKey := fmt.Sprintf("user:qrcode:user:%s", userUUID)
	pipe := r.redisClient.Pipeline()
	pipe.Get(ctx, userKey)
	pipe.TTL(ctx, userKey)
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		return "", time.Time{}, WrapRedisError(err)
	}
	token := cmds[0].(*redis.StringCmd).Val()
	expireTime := time.Now().Add(cmds[1].(*redis.DurationCmd).Val().Round(time.Second))
	return token, expireTime, nil
}

// SearchUser 搜索用户（按邮箱、昵称、UUID）
func (r *userRepositoryImpl) SearchUser(ctx context.Context, keyword string, page, pageSize int) ([]*model.UserInfo, int64, error) {
	// 计算偏移量
	offset := (page - 1) * pageSize

	// 判断关键词是否为邮箱格式（简单判断：包含@符号）
	isEmail := strings.Contains(keyword, "@")

	// 构建查询条件
	query := r.db.WithContext(ctx).
		Model(&model.UserInfo{}).
		Where("deleted_at IS NULL")

	if isEmail {
		// 邮箱格式：全匹配
		query = query.Where("email = ?", keyword)
	} else {
		// 非邮箱格式：模糊搜索（昵称、UUID）
		query = query.Where("(nickname LIKE ? OR uuid LIKE ?)",
			keyword+"%",
			keyword+"%")
	}

	// 先查询总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, WrapDBError(err)
	}

	// 查询用户列表
	var users []*model.UserInfo
	if err := query.
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&users).
		Error; err != nil {
		return nil, 0, WrapDBError(err)
	}

	return users, total, nil
}