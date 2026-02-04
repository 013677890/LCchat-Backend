package repository

import (
	"ChatServer/apps/user/mq"
	"ChatServer/consts/redisKey"
	"ChatServer/model"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// deviceRepositoryImpl 设备会话数据访问层实现
type deviceRepositoryImpl struct {
	db          *gorm.DB
	redisClient *redis.Client
}

// NewDeviceRepository 创建设备会话仓储实例
func NewDeviceRepository(db *gorm.DB, redisClient *redis.Client) IDeviceRepository {
	return &deviceRepositoryImpl{db: db, redisClient: redisClient}
}

// Redis Key 构造函数
func (r *deviceRepositoryImpl) accessTokenKey(userUUID, deviceID string) string {
	return rediskey.AccessTokenKey(userUUID, deviceID)
}

func (r *deviceRepositoryImpl) refreshTokenKey(userUUID, deviceID string) string {
	return rediskey.RefreshTokenKey(userUUID, deviceID)
}

func (r *deviceRepositoryImpl) deviceInfoKey(userUUID string) string {
	return rediskey.DeviceInfoKey(userUUID)
}

func (r *deviceRepositoryImpl) deviceActiveKey(userUUID string) string {
	return rediskey.DeviceActiveKey(userUUID)
}

type deviceCacheItem struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
	UserAgent  string `json:"userAgent,omitempty"`
	Status     int8   `json:"status"`
	LoginAt    string `json:"loginAt"` // RFC3339
}

// md5Hash 计算字符串的 MD5 哈希
func md5Hash(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// Create 创建设备会话
func (r *deviceRepositoryImpl) Create(ctx context.Context, session *model.DeviceSession) error {
	err := r.db.WithContext(ctx).Create(session).Error
	if err != nil {
		return WrapDBError(err)
	}
	return nil
}

// GetByUserUUID 获取用户的所有设备会话
func (r *deviceRepositoryImpl) GetByUserUUID(ctx context.Context, userUUID string) ([]*model.DeviceSession, error) {
	var sessions []*model.DeviceSession
	err := r.db.WithContext(ctx).
		Where("user_uuid = ?", userUUID).
		Order("updated_at DESC, id DESC").
		Find(&sessions).Error
	if err != nil {
		return nil, WrapDBError(err)
	}
	return sessions, nil
}

// GetByDeviceID 根据设备ID获取会话
func (r *deviceRepositoryImpl) GetByDeviceID(ctx context.Context, userUUID, deviceID string) (*model.DeviceSession, error) {
	var session model.DeviceSession
	err := r.db.WithContext(ctx).
		Where("user_uuid = ? AND device_id = ?", userUUID, deviceID).
		First(&session).Error
	if err != nil {
		return nil, WrapDBError(err)
	}
	return &session, nil
}

// UpsertSession 创建或更新设备会话（Upsert）
func (r *deviceRepositoryImpl) UpsertSession(ctx context.Context, session *model.DeviceSession) error {
	now := time.Now()
	onlineStatus := model.DeviceStatusOnline
	session.Status = onlineStatus

	// 直接执行 INSERT ... ON DUPLICATE KEY UPDATE
	// 当唯一索引冲突时（user_uuid + device_id 已存在），执行 UPDATE
	err := r.db.WithContext(ctx).
		Exec(`
			INSERT INTO device_session (
				user_uuid, device_id, device_name, platform, 
				app_version, ip, user_agent, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				device_name = VALUES(device_name),
				platform = VALUES(platform),
				app_version = VALUES(app_version),
				ip = VALUES(ip),
				user_agent = VALUES(user_agent),
				status = ?,
				updated_at = VALUES(updated_at)
		`,
			session.UserUuid, session.DeviceId, session.DeviceName, session.Platform,
			session.AppVersion, session.IP, session.UserAgent, onlineStatus, now, now, onlineStatus,
		).Error

	if err != nil {
		return WrapDBError(err)
	}

	r.storeDeviceInfoCache(ctx, session, now)
	return nil
}

func (r *deviceRepositoryImpl) storeDeviceInfoCache(ctx context.Context, session *model.DeviceSession, loginAt time.Time) {
	if r.redisClient == nil {
		return
	}
	cacheKey := r.deviceInfoKey(session.UserUuid)
	item := deviceCacheItem{
		DeviceID:   session.DeviceId,
		DeviceName: session.DeviceName,
		Platform:   session.Platform,
		AppVersion: session.AppVersion,
		UserAgent:  session.UserAgent,
		Status:     session.Status,
		LoginAt:    loginAt.UTC().Format(time.RFC3339),
	}
	value, err := json.Marshal(item)
	if err != nil {
		LogRedisError(ctx, err)
		return
	}

	pipe := r.redisClient.Pipeline()
	pipe.HSet(ctx, cacheKey, session.DeviceId, value)
	pipe.Expire(ctx, cacheKey, rediskey.DeviceInfoTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		cmds := []mq.RedisCmd{
			{Command: "hset", Args: []interface{}{cacheKey, session.DeviceId, value}},
			{Command: "expire", Args: []interface{}{cacheKey, int(rediskey.DeviceInfoTTL.Seconds())}},
		}
		task := mq.BuildPipelineTask(cmds).
			WithSource("DeviceRepository.storeDeviceInfoCache").
			WithMaxRetries(5)
		LogAndRetryRedisError(ctx, task, err)
	}
}

// TouchDeviceInfoTTL 续期设备信息缓存 TTL
func (r *deviceRepositoryImpl) TouchDeviceInfoTTL(ctx context.Context, userUUID string) error {
	if r.redisClient == nil {
		return nil
	}
	key := r.deviceInfoKey(userUUID)
	if err := r.redisClient.Expire(ctx, key, rediskey.DeviceInfoTTL).Err(); err != nil {
		task := mq.BuildPipelineTask([]mq.RedisCmd{
			{Command: "expire", Args: []interface{}{key, int(rediskey.DeviceInfoTTL.Seconds())}},
		}).WithSource("DeviceRepository.TouchDeviceInfoTTL").WithMaxRetries(3)
		LogAndRetryRedisError(ctx, task, err)
		return WrapRedisError(err)
	}
	return nil
}

// GetActiveTimestamps 获取设备活跃时间戳（unix 秒）
func (r *deviceRepositoryImpl) GetActiveTimestamps(ctx context.Context, userUUID string, deviceIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(deviceIDs))
	if len(deviceIDs) == 0 {
		return result, nil
	}
	if r.redisClient == nil {
		return result, nil
	}
	key := r.deviceActiveKey(userUUID)
	values, err := r.redisClient.HMGet(ctx, key, deviceIDs...).Result()
	if err != nil {
		return nil, WrapRedisError(err)
	}
	for i, v := range values {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if ts, parseErr := strconv.ParseInt(val, 10, 64); parseErr == nil {
				result[deviceIDs[i]] = ts
			}
		case []byte:
			if ts, parseErr := strconv.ParseInt(string(val), 10, 64); parseErr == nil {
				result[deviceIDs[i]] = ts
			}
		case int64:
			result[deviceIDs[i]] = val
		case int:
			result[deviceIDs[i]] = int64(val)
		}
	}
	return result, nil
}

// SetActiveTimestamp 设置设备活跃时间戳（unix 秒）并续期
func (r *deviceRepositoryImpl) SetActiveTimestamp(ctx context.Context, userUUID, deviceID string, ts int64) error {
	if r.redisClient == nil {
		return nil
	}
	key := r.deviceActiveKey(userUUID)
	pipe := r.redisClient.Pipeline()
	pipe.HSet(ctx, key, deviceID, ts)
	pipe.Expire(ctx, key, rediskey.DeviceActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		cmds := []mq.RedisCmd{
			{Command: "hset", Args: []interface{}{key, deviceID, ts}},
			{Command: "expire", Args: []interface{}{key, int(rediskey.DeviceActiveTTL.Seconds())}},
		}
		task := mq.BuildPipelineTask(cmds).
			WithSource("DeviceRepository.SetActiveTimestamp").
			WithMaxRetries(5)
		LogAndRetryRedisError(ctx, task, err)
		return WrapRedisError(err)
	}
	return nil
}

// StoreAccessToken 将 AccessToken 存入 Redis
// userUUID: 用户 UUID
// deviceID: 设备 ID
// accessToken: 访问令牌（完整的 JWT 字符串）
// expireDuration: 过期时间
func (r *deviceRepositoryImpl) StoreAccessToken(ctx context.Context, userUUID, deviceID, accessToken string, expireDuration time.Duration) error {
	key := r.accessTokenKey(userUUID, deviceID)
	// 存储 MD5 哈希值以节省内存
	value := md5Hash(accessToken)
	err := r.redisClient.Set(ctx, key, value, expireDuration).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildSetTask(key, value, expireDuration).
			WithSource("DeviceRepository.StoreAccessToken").
			WithMaxRetries(5) // AccessToken 存储重要，增加重试次数
		LogAndRetryRedisError(ctx, task, err)
		return WrapRedisError(err)
	}
	return nil
}

// StoreRefreshToken 将 RefreshToken 存入 Redis
// userUUID: 用户 UUID
// deviceID: 设备 ID
// refreshToken: 刷新令牌（UUID 字符串）
// expireDuration: 过期时间
func (r *deviceRepositoryImpl) StoreRefreshToken(ctx context.Context, userUUID, deviceID, refreshToken string, expireDuration time.Duration) error {
	key := r.refreshTokenKey(userUUID, deviceID)
	// RefreshToken 直接存储原始值
	err := r.redisClient.Set(ctx, key, refreshToken, expireDuration).Err()
	if err != nil {
		// 发送到重试队列
		task := mq.BuildSetTask(key, refreshToken, expireDuration).
			WithSource("DeviceRepository.StoreRefreshToken").
			WithMaxRetries(5) // RefreshToken 存储重要，增加重试次数
		LogAndRetryRedisError(ctx, task, err)
		return WrapRedisError(err)
	}
	return nil
}

// VerifyAccessToken 验证 AccessToken 是否有效
// 返回 true 表示 Token 有效且未被踢出
func (r *deviceRepositoryImpl) VerifyAccessToken(ctx context.Context, userUUID, deviceID, accessToken string) (bool, error) {
	key := r.accessTokenKey(userUUID, deviceID)
	storedHash, err := r.redisClient.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			// Key 不存在，说明 Token 已过期或被踢出
			return false, nil
		}
		return false, WrapRedisError(err)
	}

	// 比对 MD5 哈希
	currentHash := md5Hash(accessToken)
	return storedHash == currentHash, nil
}

// GetRefreshToken 获取 RefreshToken
func (r *deviceRepositoryImpl) GetRefreshToken(ctx context.Context, userUUID, deviceID string) (string, error) {
	key := r.refreshTokenKey(userUUID, deviceID)
	result, err := r.redisClient.Get(ctx, key).Result()
	if err != nil {
		return "", WrapRedisError(err)
	}
	return result, nil
}

// DeleteTokens 删除设备的所有 Token（用于踢出设备）
func (r *deviceRepositoryImpl) DeleteTokens(ctx context.Context, userUUID, deviceID string) error {
	if r.redisClient == nil {
		return nil
	}

	atKey := r.accessTokenKey(userUUID, deviceID)
	rtKey := r.refreshTokenKey(userUUID, deviceID)

	pipe := r.redisClient.Pipeline()
	pipe.Del(ctx, atKey)
	pipe.Del(ctx, rtKey)
	_, err := pipe.Exec(ctx)
	if err != nil {
		// 发送到重试队列（Pipeline）
		cmds := []mq.RedisCmd{
			{Command: "del", Args: []interface{}{atKey}},
			{Command: "del", Args: []interface{}{rtKey}},
		}
		task := mq.BuildPipelineTask(cmds).
			WithSource("DeviceRepository.DeleteTokens").
			WithMaxRetries(5) // Token 删除重要，增加重试次数
		LogAndRetryRedisError(ctx, task, err)
		return WrapRedisError(err)
	}
	return nil
}

// UpdateOnlineStatus 更新在线状态
func (r *deviceRepositoryImpl) UpdateOnlineStatus(ctx context.Context, userUUID, deviceID string, status int8) error {
	result := r.db.WithContext(ctx).
		Model(&model.DeviceSession{}).
		Where("user_uuid = ? AND device_id = ? AND deleted_at IS NULL", userUUID, deviceID).
		Updates(map[string]interface{}{
			"status":     status,
			"updated_at": time.Now(),
		})

	if result.Error != nil {
		return WrapDBError(result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrRecordNotFound
	}

	// Redis-first 场景下同步更新设备缓存状态，避免读到旧状态。
	if r.redisClient != nil {
		cacheKey := r.deviceInfoKey(userUUID)
		raw, err := r.redisClient.HGet(ctx, cacheKey, deviceID).Result()
		if err == nil {
			var item deviceCacheItem
			if unmarshalErr := json.Unmarshal([]byte(raw), &item); unmarshalErr == nil {
				item.Status = status
				value, marshalErr := json.Marshal(item)
				if marshalErr == nil {
					pipe := r.redisClient.Pipeline()
					pipe.HSet(ctx, cacheKey, deviceID, value)
					pipe.Expire(ctx, cacheKey, rediskey.DeviceInfoTTL)
					if _, pipeErr := pipe.Exec(ctx); pipeErr != nil && pipeErr != redis.Nil {
						LogRedisError(ctx, pipeErr)
					}
				}
			}
		} else if err != redis.Nil {
			LogRedisError(ctx, err)
		}
	}

	return nil
}

// UpdateLastSeen 更新最后活跃时间
func (r *deviceRepositoryImpl) UpdateLastSeen(ctx context.Context, userUUID, deviceID string) error {
	return nil // TODO: 更新最后活跃时间
}

// Delete 删除设备会话
func (r *deviceRepositoryImpl) Delete(ctx context.Context, userUUID, deviceID string) error {
	return nil // TODO: 删除设备会话
}

// GetOnlineDevices 获取在线设备列表
func (r *deviceRepositoryImpl) GetOnlineDevices(ctx context.Context, userUUID string) ([]*model.DeviceSession, error) {
	return nil, nil // TODO: 获取在线设备列表
}

// BatchGetOnlineStatus 批量获取用户在线状态
func (r *deviceRepositoryImpl) BatchGetOnlineStatus(ctx context.Context, userUUIDs []string) (map[string][]*model.DeviceSession, error) {
	result := make(map[string][]*model.DeviceSession, len(userUUIDs))
	if len(userUUIDs) == 0 {
		return result, nil
	}

	// 先做去重，避免重复查询同一用户。
	uniqueUsers := make([]string, 0, len(userUUIDs))
	seenUsers := make(map[string]struct{}, len(userUUIDs))
	for _, userUUID := range userUUIDs {
		if userUUID == "" {
			continue
		}
		if _, ok := seenUsers[userUUID]; ok {
			continue
		}
		seenUsers[userUUID] = struct{}{}
		uniqueUsers = append(uniqueUsers, userUUID)
	}
	if len(uniqueUsers) == 0 {
		return result, nil
	}

	// 1) Redis 优先：读取设备信息缓存 user:devices:{user_uuid}
	missedUsers := make([]string, 0, len(uniqueUsers))
	if r.redisClient != nil {
		pipe := r.redisClient.Pipeline()
		cacheCmds := make(map[string]*redis.MapStringStringCmd, len(uniqueUsers))
		for _, userUUID := range uniqueUsers {
			cacheCmds[userUUID] = pipe.HGetAll(ctx, r.deviceInfoKey(userUUID))
		}

		_, err := pipe.Exec(ctx)
		if err != nil && err != redis.Nil {
			// Redis 故障时降级到 MySQL
			LogRedisError(ctx, err)
			missedUsers = append(missedUsers, uniqueUsers...)
		} else {
			for _, userUUID := range uniqueUsers {
				entries := cacheCmds[userUUID].Val()
				if len(entries) == 0 {
					missedUsers = append(missedUsers, userUUID)
					continue
				}

				sessions := make([]*model.DeviceSession, 0, len(entries))
				parseErrCount := 0
				for _, raw := range entries {
					var item deviceCacheItem
					if err := json.Unmarshal([]byte(raw), &item); err != nil {
						parseErrCount++
						continue
					}
					sessions = append(sessions, &model.DeviceSession{
						UserUuid:   userUUID,
						DeviceId:   item.DeviceID,
						DeviceName: item.DeviceName,
						Platform:   item.Platform,
						AppVersion: item.AppVersion,
						UserAgent:  item.UserAgent,
						Status:     item.Status,
					})
				}

				// 缓存脏数据时回源 MySQL，避免返回不完整设备列表。
				if parseErrCount > 0 {
					missedUsers = append(missedUsers, userUUID)
					continue
				}
				result[userUUID] = sessions
			}
		}
	} else {
		missedUsers = append(missedUsers, uniqueUsers...)
	}

	// 2) 回源 MySQL：仅查询 Redis 未命中的用户
	if len(missedUsers) > 0 {
		var dbSessions []*model.DeviceSession
		err := r.db.WithContext(ctx).
			Where("user_uuid IN ?", missedUsers).
			Order("updated_at DESC, id DESC").
			Find(&dbSessions).Error
		if err != nil {
			return nil, WrapDBError(err)
		}

		dbGrouped := make(map[string][]*model.DeviceSession, len(missedUsers))
		for _, session := range dbSessions {
			if session == nil || session.UserUuid == "" {
				continue
			}
			dbGrouped[session.UserUuid] = append(dbGrouped[session.UserUuid], session)
		}

		for _, userUUID := range missedUsers {
			// 未查到时保持零值（nil 切片），上层按离线处理。
			result[userUUID] = dbGrouped[userUUID]
		}

		// 3) 尽力回填 Redis 缓存（不阻塞主流程）
		if r.redisClient != nil && len(dbSessions) > 0 {
			pipe := r.redisClient.Pipeline()
			touchedUsers := make(map[string]struct{}, len(dbSessions))
			for _, session := range dbSessions {
				if session == nil || session.UserUuid == "" || session.DeviceId == "" {
					continue
				}
				item := deviceCacheItem{
					DeviceID:   session.DeviceId,
					DeviceName: session.DeviceName,
					Platform:   session.Platform,
					AppVersion: session.AppVersion,
					UserAgent:  session.UserAgent,
					Status:     session.Status,
					LoginAt:    session.UpdatedAt.UTC().Format(time.RFC3339),
				}
				value, mErr := json.Marshal(item)
				if mErr != nil {
					continue
				}

				key := r.deviceInfoKey(session.UserUuid)
				pipe.HSet(ctx, key, session.DeviceId, value)
				touchedUsers[session.UserUuid] = struct{}{}
			}
			for userUUID := range touchedUsers {
				pipe.Expire(ctx, r.deviceInfoKey(userUUID), rediskey.DeviceInfoTTL)
			}
			if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
				LogRedisError(ctx, err)
			}
		}
	}

	// 补齐空键，确保每个请求用户在返回 map 中都有条目。
	for _, userUUID := range uniqueUsers {
		if _, ok := result[userUUID]; !ok {
			result[userUUID] = nil
		}
	}

	return result, nil
}

// UpdateToken 更新Token
func (r *deviceRepositoryImpl) UpdateToken(ctx context.Context, userUUID, deviceID, token, refreshToken string, expireAt *time.Time) error {
	return nil // TODO: 更新Token
}

// DeleteByUserUUID 删除用户所有设备会话
func (r *deviceRepositoryImpl) DeleteByUserUUID(ctx context.Context, userUUID string) error {
	return nil // TODO: 删除用户所有设备会话
}
