package svc

import (
	userpb "ChatServer/apps/user/pb"
	rediskey "ChatServer/consts/redisKey"
	"ChatServer/model"
	"ChatServer/pkg/logger"
	"context"
	"time"
)

const (
	// activeThrottleInterval 心跳活跃时间写 Redis 的最小间隔。
	// 在该窗口内的重复心跳只更新本地缓存，不触发 Redis 写入，
	// 以降低高频心跳对 Redis 的写压力。
	activeThrottleInterval = 5 * time.Minute

	// deviceStatusRPCTimeout UpdateDeviceStatus RPC 调用超时时间。
	deviceStatusRPCTimeout = 3 * time.Second

	// statusWorkerCount 设备状态 RPC 工作协程数量。
	statusWorkerCount = 64
	// statusQueueSize 设备状态 RPC 任务队列容量。
	// 队列满时新任务会被丢弃（仅 log Warn），不会阻塞调用方。
	statusQueueSize = 8192
)

// deviceStatusTask 表示一条设备状态更新 RPC 任务。
type deviceStatusTask struct {
	logCtx   context.Context // 仅用于日志上下文，不用于 RPC 超时
	userUUID string
	deviceID string
	status   int8
}

// OnConnect 在连接建立后触发。
// 行为：
// 1. 立即写入设备活跃时间（用于在线状态判定），不受节流限制；
// 2. 异步调用 user-service RPC 将 DeviceSession.status 置为在线。
func (s *ConnectService) OnConnect(ctx context.Context, session *Session) {
	s.touchActive(ctx, session.UserUUID, session.DeviceID, true)
	s.updateDeviceStatusAsync(ctx, session, model.DeviceStatusOnline)
}

// OnHeartbeat 在收到客户端心跳后触发。
// 受 5 分钟本地节流保护：窗口内的重复心跳不会触发 Redis 写入。
func (s *ConnectService) OnHeartbeat(ctx context.Context, session *Session) {
	s.touchActive(ctx, session.UserUUID, session.DeviceID, false)
}

// OnDisconnect 在连接断开后触发。
// 行为：
// 1. 清理本地节流缓存，避免内存泄漏；
// 2. 异步调用 user-service RPC 将 DeviceSession.status 置为离线。
func (s *ConnectService) OnDisconnect(ctx context.Context, session *Session) {
	throttleKey := session.UserUUID + ":" + session.DeviceID
	s.activeThrottle.Delete(throttleKey)
	s.updateDeviceStatusAsync(ctx, session, model.DeviceStatusOffline)
}

// updateDeviceStatusAsync 将设备状态更新任务投递到工作队列。
// 降级策略：
// - statusQueue 为 nil 时静默跳过（userDeviceClient 不可用）。
// - 队列满时丢弃任务，仅 log Warn，不阻塞调用方。
func (s *ConnectService) updateDeviceStatusAsync(ctx context.Context, session *Session, status int8) {
	if s.statusQueue == nil {
		return
	}

	task := deviceStatusTask{
		logCtx:   ctx,
		userUUID: session.UserUUID,
		deviceID: session.DeviceID,
		status:   status,
	}

	select {
	case s.statusQueue <- task:
		// 成功投递
	default:
		// 队列满，丢弃任务
		logger.Warn(ctx, "设备状态更新队列已满，丢弃任务",
			logger.String("user_uuid", task.userUUID),
			logger.String("device_id", task.deviceID),
			logger.Int("status", int(task.status)),
		)
	}
}

// statusWorker 从队列消费任务，执行设备状态 RPC 调用。
// 每个任务独立创建 3s 超时上下文，失败仅 log Warn。
func (s *ConnectService) statusWorker() {
	defer s.statusWg.Done()

	for task := range s.statusQueue {
		rpcCtx, cancel := context.WithTimeout(context.Background(), deviceStatusRPCTimeout)

		_, err := s.userDeviceClient.UpdateDeviceStatus(rpcCtx, &userpb.UpdateDeviceStatusRequest{
			UserUuid: task.userUUID,
			DeviceId: task.deviceID,
			Status:   int32(task.status),
		})
		if err != nil {
			logger.Warn(task.logCtx, "UpdateDeviceStatus RPC 调用失败（不影响连接）",
				logger.String("user_uuid", task.userUUID),
				logger.String("device_id", task.deviceID),
				logger.Int("status", int(task.status)),
				logger.ErrorField("error", err),
			)
		}

		cancel()
	}
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
