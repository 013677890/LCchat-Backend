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

// friendRepositoryImpl 好友关系数据访问层实现
type friendRepositoryImpl struct {
	db          *gorm.DB
	redisClient *redis.Client
}

// NewFriendRepository 创建好友关系仓储实例
func NewFriendRepository(db *gorm.DB, redisClient *redis.Client) IFriendRepository {
	return &friendRepositoryImpl{db: db, redisClient: redisClient}
}

// GetFriendList 获取好友列表
func (r *friendRepositoryImpl) GetFriendList(ctx context.Context, userUUID, groupTag string, page, pageSize int) ([]*model.UserRelation, int64, error) {
	return nil, 0, nil // TODO: 实现获取好友列表
}

// GetFriendRelation 获取好友关系
func (r *friendRepositoryImpl) GetFriendRelation(ctx context.Context, userUUID, friendUUID string) (*model.UserRelation, error) {
	return nil, nil // TODO: 实现获取好友关系
}

// CreateFriendRelation 创建好友关系（双向）
func (r *friendRepositoryImpl) CreateFriendRelation(ctx context.Context, userUUID, friendUUID string) error {
	return nil // TODO: 实现创建好友关系
}

// DeleteFriendRelation 删除好友关系（单向）
func (r *friendRepositoryImpl) DeleteFriendRelation(ctx context.Context, userUUID, friendUUID string) error {
	return nil // TODO: 实现删除好友关系
}

// SetFriendRemark 设置好友备注
func (r *friendRepositoryImpl) SetFriendRemark(ctx context.Context, userUUID, friendUUID, remark string) error {
	return nil // TODO: 设置好友备注
}

// SetFriendTag 设置好友标签
func (r *friendRepositoryImpl) SetFriendTag(ctx context.Context, userUUID, friendUUID, groupTag string) error {
	return nil // TODO: 设置好友标签
}

// GetTagList 获取标签列表
func (r *friendRepositoryImpl) GetTagList(ctx context.Context, userUUID string) ([]string, error) {
	return nil, nil // TODO: 获取标签列表
}

// IsFriend 检查是否是好友
// 采用 Cache-Aside Pattern：优先查 Redis Set，未命中则回源 MySQL 并缓存
func (r *friendRepositoryImpl) IsFriend(ctx context.Context, userUUID, friendUUID string) (bool, error) {
	cacheKey := fmt.Sprintf("user:relation:friend:%s", userUUID)

	// ==================== 1. 组合查询 Redis (Pipeline) ====================
	// 使用 Pipeline 一次性发送命令，减少网络 RTT
	pipe := r.redisClient.Pipeline()

	// 命令1: 检查 Key 是否存在 (区分缓存命中/未命中)
	existsCmd := pipe.Exists(ctx, cacheKey)
	// 命令2: 检查是否是好友 (只有 Key 存在时此结果才有效)
	isMemberCmd := pipe.SIsMember(ctx, cacheKey, friendUUID)

	// 概率续期优化：1% 的概率在读取时顺便续期
	// 无论 Key 是否存在，Expire 都是安全的 (不存在则返回0)
	if getRandomBool(0.01) {
		pipe.Expire(ctx, cacheKey, getRandomExpireTime(24*time.Hour))
	}

	_, err := pipe.Exec(ctx)

	if err != nil && err != redis.Nil {
		// Redis 挂了，记录日志，降级去查 DB
		LogRedisError(ctx, err)
	} else if err == nil {
		// Redis 正常返回
		// 核心逻辑：先看 Key 在不在
		if existsCmd.Val() > 0 {
			// Case A: 缓存命中 (Hit)
			// 此时 Redis 是权威的。SIsMember 说 false 就是 false (绝对非好友)。
			// 注意：哪怕 Set 里只有 "__EMPTY__"，SIsMember 也会正确返回 false。
			return isMemberCmd.Val(), nil
		}
		// Case B: 缓存未命中 (Miss) -> Exists 返回 0
		// 代码继续往下走，去查数据库
	}

	// ==================== 2. 缓存未命中，回源查询 MySQL ====================
	var relations []model.UserRelation
	err = r.db.WithContext(ctx).
		Where("user_uuid = ? AND status = ? AND deleted_at IS NULL", userUUID, 0).
		Find(&relations).Error

	if err != nil {
		return false, WrapDBError(err)
	}

	// ==================== 3. 重建缓存 (保持 Set 类型) ====================
	pipe = r.redisClient.Pipeline()
	pipe.Del(ctx, cacheKey) // 清理旧数据

	if len(relations) == 0 {
		// [修复类型冲突] 空列表也用 Set，写入特殊标记
		pipe.SAdd(ctx, cacheKey, "__EMPTY__")
		// 空值缓存时间短一点 (5分钟)
		pipe.Expire(ctx, cacheKey, 5*time.Minute)
	} else {
		// 提取 UUID
		friendUUIDs := make([]interface{}, len(relations))
		// 优化：顺便在内存里判断一下结果，省得最后再遍历
		isFriendFound := false
		for i, relation := range relations {
			friendUUIDs[i] = relation.PeerUuid
			if relation.PeerUuid == friendUUID {
				isFriendFound = true
			}
		}

		pipe.SAdd(ctx, cacheKey, friendUUIDs...)
		pipe.Expire(ctx, cacheKey, getRandomExpireTime(24*time.Hour))

		// 异步执行写入，不需要等待结果，让接口响应更快
		if _, err := pipe.Exec(ctx); err != nil {
			// 发送到重试队列
			cmds := []mq.RedisCmd{
				{Command: "del", Args: []interface{}{cacheKey}},
				{Command: "sadd", Args: append([]interface{}{cacheKey}, friendUUIDs...)},
				{Command: "expire", Args: []interface{}{cacheKey, int(getRandomExpireTime(24 * time.Hour).Seconds())}},
			}
			task := mq.BuildPipelineTask(cmds).
				WithSource("FriendRepository.IsFriend.RebuildCache")
			LogAndRetryRedisError(ctx, task, err)
		}

		return isFriendFound, nil
	}

	// 执行空值的 Pipeline
	if _, err := pipe.Exec(ctx); err != nil {
		// 发送到重试队列
		cmds := []mq.RedisCmd{
			{Command: "del", Args: []interface{}{cacheKey}},
			{Command: "sadd", Args: []interface{}{cacheKey, "__EMPTY__"}},
			{Command: "expire", Args: []interface{}{cacheKey, int((5 * time.Minute).Seconds())}},
		}
		task := mq.BuildPipelineTask(cmds).
			WithSource("FriendRepository.IsFriend.RebuildEmptyCache")
		LogAndRetryRedisError(ctx, task, err)
	}

	// 如果是空列表，那肯定不是好友
	return false, nil
}

// GetRelationStatus 获取关系状态
func (r *friendRepositoryImpl) GetRelationStatus(ctx context.Context, userUUID, peerUUID string) (*model.UserRelation, error) {
	return nil, nil // TODO: 获取关系状态
}

// SyncFriendList 增量同步好友列表
func (r *friendRepositoryImpl) SyncFriendList(ctx context.Context, userUUID string, version int64, limit int) ([]*model.UserRelation, int64, error) {
	return nil, 0, nil // TODO: 增量同步好友列表
}

// BatchCheckIsFriend 批量检查是否为好友（使用Redis Set优化）
// 返回：map[peerUUID]isFriend
func (r *friendRepositoryImpl) BatchCheckIsFriend(ctx context.Context, userUUID string, peerUUIDs []string) (map[string]bool, error) {
	if len(peerUUIDs) == 0 {
		return make(map[string]bool), nil
	}

	// 构建 Redis Set key
	cacheKey := fmt.Sprintf("user:relation:friend:%s", userUUID)

	// ==================== 1. 组合查询 Redis (Pipeline) ====================
	// 优化：使用多个 SIsMember 而不是 SMembers
	// 好处：用户有 2000 好友，只查 2 人时，网络传输从 2000 个 UUID → 2 个 bool
	pipe := r.redisClient.Pipeline()

	// 命令1: 检查 Key 是否存在 (区分缓存命中/未命中)
	existsCmd := pipe.Exists(ctx, cacheKey)

	// 命令2: 批量检查每个 peerUUID 是否是好友
	isMemberCmds := make([]*redis.BoolCmd, len(peerUUIDs))
	for i, peerUUID := range peerUUIDs {
		isMemberCmds[i] = pipe.SIsMember(ctx, cacheKey, peerUUID)
	}

	// 概率续期优化：1% 的概率在读取时顺便续期
	// 无论 Key 是否存在，Expire 都是安全的 (不存在则返回0)
	if getRandomBool(0.01) {
		pipe.Expire(ctx, cacheKey, getRandomExpireTime(24*time.Hour))
	}

	_, err := pipe.Exec(ctx)

	if err != nil && err != redis.Nil {
		// Redis 挂了，记录日志，降级去查 DB
		LogRedisError(ctx, err)
	} else if err == nil {
		// Redis 正常返回
		// 核心逻辑：先看 Key 在不在
		if existsCmd.Val() > 0 {
			// Case A: 缓存命中 (Hit)
			// 此时 Redis 是权威的，直接返回结果
			result := make(map[string]bool, len(peerUUIDs))
			for i, peerUUID := range peerUUIDs {
				// 如果 SIsMember 出错，保守返回 false（后续会降级查 DB）
				if isMemberCmds[i].Err() != nil {
					LogRedisError(ctx, isMemberCmds[i].Err())
					result[peerUUID] = false
				} else {
					result[peerUUID] = isMemberCmds[i].Val()
				}
			}
			return result, nil
		}
		// Case B: 缓存未命中 (Miss) -> Exists 返回 0
		// 代码继续往下走，去查数据库
	}

	// ==================== 2. 缓存未命中，回源查询 MySQL ====================
	var relations []model.UserRelation
	err = r.db.WithContext(ctx).
		Where("user_uuid = ? AND status = ? AND deleted_at IS NULL", userUUID, 0).
		Find(&relations).Error

	if err != nil {
		return nil, WrapDBError(err)
	}

	// ==================== 3. 统一重建缓存 (保持 Set 类型) ====================
	// 优化：合并空列表和非空列表的 Pipeline 逻辑，避免代码重复
	pipe = r.redisClient.Pipeline()
	pipe.Del(ctx, cacheKey) // 清理旧数据

	var cmds []mq.RedisCmd

	if len(relations) == 0 {
		// 空列表也用 Set，写入特殊标记
		pipe.SAdd(ctx, cacheKey, "__EMPTY__")
		// 空值缓存时间短一点 (5分钟)
		pipe.Expire(ctx, cacheKey, 5*time.Minute)

		// 构建重试队列命令
		cmds = []mq.RedisCmd{
			{Command: "del", Args: []interface{}{cacheKey}},
			{Command: "sadd", Args: []interface{}{cacheKey, "__EMPTY__"}},
			{Command: "expire", Args: []interface{}{cacheKey, int((5 * time.Minute).Seconds())}},
		}
	} else {
		// 提取 UUID
		friendUUIDs := make([]interface{}, len(relations))
		for i, relation := range relations {
			friendUUIDs[i] = relation.PeerUuid
		}

		pipe.SAdd(ctx, cacheKey, friendUUIDs...)
		pipe.Expire(ctx, cacheKey, getRandomExpireTime(24*time.Hour))

		// 构建重试队列命令
		cmds = []mq.RedisCmd{
			{Command: "del", Args: []interface{}{cacheKey}},
			{Command: "sadd", Args: append([]interface{}{cacheKey}, friendUUIDs...)},
			{Command: "expire", Args: []interface{}{cacheKey, int(getRandomExpireTime(24 * time.Hour).Seconds())}},
		}
	}

	// 异步执行写入，不需要等待结果，让接口响应更快
	if _, err := pipe.Exec(ctx); err != nil {
		// 发送到重试队列
		task := mq.BuildPipelineTask(cmds).WithSource("FriendRepository.BatchCheckIsFriend.RebuildCache")
		LogAndRetryRedisError(ctx, task, err)
	}

	// ==================== 4. 构建返回结果 ====================
	// 将 DB 查询到的好友集合转为 map
	friendSet := make(map[string]bool, len(relations))
	for _, relation := range relations {
		friendSet[relation.PeerUuid] = true
	}

	// 构建返回结果
	result := make(map[string]bool, len(peerUUIDs))
	for _, peerUUID := range peerUUIDs {
		result[peerUUID] = friendSet[peerUUID]
	}

	return result, nil
}
