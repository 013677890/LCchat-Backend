package svc

import (
	"ChatServer/pkg/logger"
	"ChatServer/pkg/util"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"

	rediskey "ChatServer/consts/redisKey"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrTokenRequired 表示握手参数中缺少 token。
	ErrTokenRequired = errors.New("token is required")
	// ErrDeviceIDRequired 表示握手参数中缺少 device_id。
	ErrDeviceIDRequired = errors.New("device_id is required")
	// ErrTokenInvalid 表示 token 非法、已过期，或与设备不匹配。
	ErrTokenInvalid = errors.New("token is invalid")
)

// Authenticate 校验 WebSocket 握手参数与登录态。
// 校验流程：
// 1. 校验 token/device_id 是否为空；
// 2. 解析 JWT，校验 claims 基本字段；
// 3. 强校验 claims.DeviceID 与 query.device_id 一致；
// 4. 若 Redis 可用，校验 auth:at:{user_uuid}:{device_id} 中存储的 token md5。
//
// 降级策略（Fail-Open）：
// - 当 Redis 异常不可用时，不直接拒绝连接，而是退化为仅 JWT 校验；
// - 这样可提升可用性，但会降低"被踢立即失效"的严格性。
func (s *ConnectService) Authenticate(ctx context.Context, token, deviceID, clientIP string) (*Session, error) {
	token = strings.TrimSpace(token)
	deviceID = strings.TrimSpace(deviceID)
	clientIP = strings.TrimSpace(clientIP)

	if token == "" {
		return nil, ErrTokenRequired
	}
	if deviceID == "" {
		return nil, ErrDeviceIDRequired
	}

	claims, err := util.ParseToken(token)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	if claims.UserUUID == "" || claims.DeviceID == "" || claims.DeviceID != deviceID {
		return nil, ErrTokenInvalid
	}

	// 与 user/auth 存储规则保持一致：
	// auth:at:{user_uuid}:{device_id} = md5(access_token)
	if s.redisClient != nil {
		key := rediskey.AccessTokenKey(claims.UserUUID, claims.DeviceID)
		storedHash, getErr := s.redisClient.Get(ctx, key).Result()
		switch {
		case getErr == redis.Nil:
			return nil, ErrTokenInvalid
		case getErr != nil:
			// Redis 短暂故障时采用 fail-open，优先保证连接服务可用性。
			logger.Warn(ctx, "连接鉴权读取 Redis 失败，降级为仅 JWT 校验",
				logger.String("user_uuid", claims.UserUUID),
				logger.String("device_id", claims.DeviceID),
				logger.ErrorField("error", getErr),
			)
		default:
			if storedHash != md5Hex(token) {
				return nil, ErrTokenInvalid
			}
		}
	}

	return &Session{
		UserUUID: claims.UserUUID,
		DeviceID: claims.DeviceID,
		ClientIP: clientIP,
	}, nil
}

// md5Hex 返回字符串的 MD5 十六进制摘要。
// 用于与 auth 服务中存储的 access_token 哈希值进行比较。
func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}
