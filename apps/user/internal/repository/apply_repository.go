package repository

import (
	"ChatServer/apps/user/mq"
	"ChatServer/model"
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// applyRepositoryImpl 好友申请数据访问层实现
type applyRepositoryImpl struct {
	db          *gorm.DB
	redisClient *redis.Client
}

// NewApplyRepository 创建好友申请仓储实例
func NewApplyRepository(db *gorm.DB, redisClient *redis.Client) IApplyRepository {
	return &applyRepositoryImpl{db: db, redisClient: redisClient}
}

// Create 创建好友申请
func (r *applyRepositoryImpl) Create(ctx context.Context, apply *model.ApplyRequest) (*model.ApplyRequest, error) {
	err := r.db.WithContext(ctx).Create(apply).Error
	if err != nil {
		return nil, WrapDBError(err)
	}

	// 尽力而为地更新目标用户的待处理申请缓存。
	// 关键原则：只有 Key 存在时才增量添加，Key 不存在时不操作（让读接口负责全量加载）。
	// 这避免了 Key 过期后增量添加导致缓存数据不完整的问题。
	cacheKey := fmt.Sprintf("user:apply:pending:%s", apply.TargetUuid)

	// 使用 Lua 脚本原子性地：检查 Key 存在 -> 移除占位符 -> 添加新成员 -> 续期
	luaScript := redis.NewScript(`
		if redis.call('EXISTS', KEYS[1]) == 1 then
			redis.call('ZREM', KEYS[1], '__EMPTY__')
			redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
			redis.call('EXPIRE', KEYS[1], ARGV[3])
			return 1
		end
		return 0
	`)

	expireSeconds := int(getRandomExpireTime(24 * time.Hour).Seconds())
	_, err = luaScript.Run(ctx, r.redisClient,
		[]string{cacheKey},
		apply.CreatedAt.Unix(),
		apply.ApplicantUuid,
		expireSeconds,
	).Result()

	if err != nil && err != redis.Nil {
		// Lua 脚本执行失败，记录日志但不阻塞主流程
		// 注意：Key 不存在返回 0 不是错误，读接口会负责全量加载
		LogRedisError(ctx, err)
	}

	return apply, nil
}

// GetByID 根据ID获取好友申请
func (r *applyRepositoryImpl) GetByID(ctx context.Context, id int64) (*model.ApplyRequest, error) {
	return nil, nil // TODO: 根据ID获取好友申请
}

// GetPendingList 获取待处理的好友申请列表
func (r *applyRepositoryImpl) GetPendingList(ctx context.Context, targetUUID string, status, page, pageSize int) ([]*model.ApplyRequest, int64, error) {
	return nil, 0, nil // TODO: 获取待处理的好友申请列表
}

// GetSentList 获取发出的好友申请列表
func (r *applyRepositoryImpl) GetSentList(ctx context.Context, applicantUUID string, status, page, pageSize int) ([]*model.ApplyRequest, int64, error) {
	return nil, 0, nil // TODO: 获取发出的好友申请列表
}

// UpdateStatus 更新申请状态
func (r *applyRepositoryImpl) UpdateStatus(ctx context.Context, id int64, status int, remark string) error {
	return nil // TODO: 更新申请状态
}

// MarkAsRead 标记申请已读
func (r *applyRepositoryImpl) MarkAsRead(ctx context.Context, ids []int64) error {
	return nil // TODO: 标记申请已读
}

// GetUnreadCount 获取未读申请数量
func (r *applyRepositoryImpl) GetUnreadCount(ctx context.Context, targetUUID string) (int64, error) {
	return 0, nil // TODO: 获取未读申请数量
}

// ExistsPendingRequest 检查是否存在待处理的申请
// 采用 Cache-Aside Pattern：优先查 Redis ZSet，未命中则回源 MySQL 并缓存
// 使用 ZSet 存储目标用户的待处理申请，以申请时间戳为 score
func (r *applyRepositoryImpl) ExistsPendingRequest(ctx context.Context, applicantUUID, targetUUID string) (bool, error) {
	cacheKey := fmt.Sprintf("user:apply:pending:%s", targetUUID)

	// ==================== 1. 组合查询 Redis (Pipeline) ====================
	pipe := r.redisClient.Pipeline()
	existsCmd := pipe.Exists(ctx, cacheKey)
	scoreCmd := pipe.ZScore(ctx, cacheKey, applicantUUID)

	// 概率续期优化：1% 的概率在读取时顺便续期
	if getRandomBool(0.01) {
		pipe.Expire(ctx, cacheKey, getRandomExpireTime(24*time.Hour))
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		LogRedisError(ctx, err)
	} else if err == nil {
		if existsCmd.Val() > 0 {
			// Cache hit: if member exists, it has a score.
			if scoreCmd.Err() == nil {
				return true, nil
			}
			if scoreCmd.Err() == redis.Nil {
				return false, nil
			}
			LogRedisError(ctx, scoreCmd.Err())
		}
	}

	// ==================== 2. 缓存未命中，回源查询 MySQL ====================
	var applies []model.ApplyRequest
	err = r.db.WithContext(ctx).
		Where("apply_type = ? AND target_uuid = ? AND status = ? AND deleted_at IS NULL", 0, targetUUID, 0).
		Find(&applies).Error
	if err != nil {
		return false, WrapDBError(err)
	}

	// ==================== 3. 重建缓存 (ZSet) ====================
	pipe = r.redisClient.Pipeline()
	pipe.Del(ctx, cacheKey)

	var cmds []mq.RedisCmd
	if len(applies) == 0 {
		pipe.ZAdd(ctx, cacheKey, redis.Z{
			Score:  0,
			Member: "__EMPTY__",
		})
		pipe.Expire(ctx, cacheKey, 5*time.Minute)
		cmds = []mq.RedisCmd{
			{Command: "del", Args: []interface{}{cacheKey}},
			{Command: "zadd", Args: []interface{}{cacheKey, 0, "__EMPTY__"}},
			{Command: "expire", Args: []interface{}{cacheKey, int((5 * time.Minute).Seconds())}},
		}
	} else {
		zs := make([]redis.Z, 0, len(applies))
		for _, apply := range applies {
			zs = append(zs, redis.Z{
				Score:  float64(apply.CreatedAt.Unix()),
				Member: apply.ApplicantUuid,
			})
		}
		pipe.ZAdd(ctx, cacheKey, zs...)
		pipe.Expire(ctx, cacheKey, getRandomExpireTime(24*time.Hour))

		cmds = []mq.RedisCmd{
			{Command: "del", Args: []interface{}{cacheKey}},
		}
		for _, apply := range applies {
			cmds = append(cmds, mq.RedisCmd{
				Command: "zadd",
				Args:    []interface{}{cacheKey, apply.CreatedAt.Unix(), apply.ApplicantUuid},
			})
		}
		cmds = append(cmds, mq.RedisCmd{
			Command: "expire",
			Args:    []interface{}{cacheKey, int(getRandomExpireTime(24 * time.Hour).Seconds())},
		})
	}

	if _, err := pipe.Exec(ctx); err != nil {
		task := mq.BuildPipelineTask(cmds).WithSource("ApplyRepository.ExistsPendingRequest.RebuildCache")
		LogAndRetryRedisError(ctx, task, err)
	}

	// ==================== 4. 根据回源结果判断 ====================
	for _, apply := range applies {
		if apply.ApplicantUuid == applicantUUID {
			return true, nil
		}
	}
	return false, nil
}

// GetByIDWithInfo 根据ID获取好友申请（包含申请人信息）
func (r *applyRepositoryImpl) GetByIDWithInfo(ctx context.Context, id int64) (*model.ApplyRequest, *model.UserInfo, error) {
	return nil, nil, nil // TODO: 根据ID获取好友申请（包含申请人信息）
}
