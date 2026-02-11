package svc

import (
	rediskey "ChatServer/consts/redisKey"
	"ChatServer/pkg/logger"
	"ChatServer/pkg/util"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

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

// Session 保存连接鉴权后的身份信息。
// 该结构会在整个连接生命周期中复用，避免重复解析 token。
type Session struct {
	UserUUID string
	DeviceID string
	ClientIP string
}

// Envelope 定义 WebSocket 通用消息包格式。
// 约定：
// - Type: 消息类型（如 heartbeat/message）；
// - Data: 消息体（由上层按 Type 再解析）。
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ErrorData 定义 type=error 时的 data 结构。
type ErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	// activeThrottleInterval 心跳活跃时间写 Redis 的最小间隔。
	// 在该窗口内的重复心跳只更新本地缓存，不触发 Redis 写入，
	// 以降低高频心跳对 Redis 的写压力。
	activeThrottleInterval = 5 * time.Minute
)

// ConnectService 承载 connect 的核心业务逻辑。
// 说明：当前只依赖 Redis，后续会补充 user/msg gRPC 客户端。
type ConnectService struct {
	redisClient    *redis.Client
	activeThrottle sync.Map // key: "userUUID:deviceID" → value: int64(上次写 Redis 的 unix 秒)
}

// NewConnectService 创建业务服务实例。
func NewConnectService(redisClient *redis.Client) *ConnectService {
	return &ConnectService{redisClient: redisClient}
}

// Authenticate 校验 WebSocket 握手参数与登录态。
// 校验流程：
// 1. 校验 token/device_id 是否为空；
// 2. 解析 JWT，校验 claims 基本字段；
// 3. 强校验 claims.DeviceID 与 query.device_id 一致；
// 4. 若 Redis 可用，校验 auth:at:{user_uuid}:{device_id} 中存储的 token md5。
//
// 降级策略（Fail-Open）：
// - 当 Redis 异常不可用时，不直接拒绝连接，而是退化为仅 JWT 校验；
// - 这样可提升可用性，但会降低“被踢立即失效”的严格性。
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

// OnConnect 在连接建立后触发。
// 当前行为：
// - 立即写入设备活跃时间（用于在线状态判定），不受节流限制；
// 后续会扩展：
// - 调 user 内部 RPC 将设备状态置为在线。
func (s *ConnectService) OnConnect(ctx context.Context, session *Session) {
	s.touchActive(ctx, session.UserUUID, session.DeviceID, true)
	// TODO: 调用 user-service 内部 RPC，将设备状态更新为在线。
}

// OnHeartbeat 在收到客户端心跳后触发。
// 受 5 分钟本地节流保护：窗口内的重复心跳不会触发 Redis 写入。
func (s *ConnectService) OnHeartbeat(ctx context.Context, session *Session) {
	s.touchActive(ctx, session.UserUUID, session.DeviceID, false)
}

// OnDisconnect 在连接断开后触发。
// 清理本地节流缓存，避免内存泄漏。
// 后续会调用 user 内部 RPC，将设备状态更新为离线。
func (s *ConnectService) OnDisconnect(ctx context.Context, session *Session) {
	throttleKey := session.UserUUID + ":" + session.DeviceID
	s.activeThrottle.Delete(throttleKey)
	// TODO: 调用 user-service 内部 RPC，将设备状态更新为离线。
}

// ParseEnvelope 解析客户端上行帧。
// 若 type 缺失或 JSON 不合法，会返回错误交由 handler 返回 error 帧。
func (s *ConnectService) ParseEnvelope(raw []byte) (*Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	envelope.Type = strings.TrimSpace(envelope.Type)
	if envelope.Type == "" {
		return nil, errors.New("type is required")
	}
	return &envelope, nil
}

// MarshalEnvelope 组装并序列化下行帧。
// 约定：data=nil 时省略 data 字段，避免无意义空对象。
func (s *ConnectService) MarshalEnvelope(msgType string, data any) ([]byte, error) {
	envelope := map[string]any{
		"type": msgType,
	}
	if data != nil {
		envelope["data"] = data
	}
	return json.Marshal(envelope)
}

// touchActive 更新设备活跃时间到 Redis。
// Key 规则：
// - key:   user:devices:active:{user_uuid}
// - field: device_id
// - value: unix 秒
//
// 节流策略：
// - force=true 时立即写入（用于 OnConnect 等必须即时生效的场景）；
// - force=false 时，若距上次写入不足 activeThrottleInterval（5 分钟），则跳过。
func (s *ConnectService) touchActive(ctx context.Context, userUUID, deviceID string, force bool) {
	if s.redisClient == nil || userUUID == "" || deviceID == "" {
		return
	}

	now := time.Now()
	throttleKey := userUUID + ":" + deviceID

	if !force {
		if last, ok := s.activeThrottle.Load(throttleKey); ok {
			if now.Sub(time.Unix(last.(int64), 0)) < activeThrottleInterval {
				return
			}
		}
	}

	key := rediskey.DeviceActiveKey(userUUID)
	ts := now.Unix()
	pipe := s.redisClient.Pipeline()
	pipe.HSet(ctx, key, deviceID, ts)
	pipe.Expire(ctx, key, rediskey.DeviceActiveTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		logger.Warn(ctx, "更新设备活跃时间失败",
			logger.String("user_uuid", userUUID),
			logger.String("device_id", deviceID),
			logger.ErrorField("error", err),
		)
		return
	}

	s.activeThrottle.Store(throttleKey, ts)
}

// md5Hex 返回字符串的 MD5 十六进制摘要。
// 用于与 auth 服务中存储的 access_token 哈希值进行比较。
func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}
