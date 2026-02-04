package service

import (
	"ChatServer/apps/user/internal/repository"
	pb "ChatServer/apps/user/pb"
	"ChatServer/consts"
	"ChatServer/model"
	"ChatServer/pkg/logger"
	"ChatServer/pkg/util"
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// deviceServiceImpl 设备会话服务实现
type deviceServiceImpl struct {
	deviceRepo repository.IDeviceRepository
}

const (
	// 设备在线判定窗口：15 分钟。
	// 网关活跃时间写入为 10 分钟节流，窗口需大于节流间隔以降低误判。
	deviceOnlineWindow = 15 * time.Minute
)

// NewDeviceService 创建设备服务实例
func NewDeviceService(deviceRepo repository.IDeviceRepository) DeviceService {
	return &deviceServiceImpl{
		deviceRepo: deviceRepo,
	}
}

// GetDeviceList 获取设备列表
func (s *deviceServiceImpl) GetDeviceList(ctx context.Context, req *pb.GetDeviceListRequest) (*pb.GetDeviceListResponse, error) {
	userUUID := util.GetUserUUIDFromContext(ctx)
	if userUUID == "" {
		logger.Warn(ctx, "获取设备列表失败：user_uuid 为空")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	deviceID := util.GetDeviceIDFromContext(ctx)

	sessionsByUser, err := s.deviceRepo.BatchGetOnlineStatus(ctx, []string{userUUID})
	if err != nil {
		logger.Error(ctx, "获取设备列表失败",
			logger.String("user_uuid", userUUID),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}
	sessions := sessionsByUser[userUUID]

	deviceIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		deviceIDs = append(deviceIDs, session.DeviceId)
	}

	activeTimes, err := s.deviceRepo.GetActiveTimestamps(ctx, userUUID, deviceIDs)
	if err != nil {
		logger.Warn(ctx, "获取设备活跃时间失败，使用当前时间兜底",
			logger.String("user_uuid", userUUID),
			logger.ErrorField("error", err),
		)
		activeTimes = map[string]int64{}
	}

	devices := make([]*pb.DeviceItem, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		sec, ok := activeTimes[session.DeviceId]
		if !ok || sec <= 0 {
			sec = time.Now().Unix()
			if err := s.deviceRepo.SetActiveTimestamp(ctx, userUUID, session.DeviceId, sec); err != nil {
				logger.Warn(ctx, "补写设备活跃时间失败",
					logger.String("user_uuid", userUUID),
					logger.String("device_id", session.DeviceId),
					logger.ErrorField("error", err),
				)
			}
		}
		lastSeenAt := sec * 1000
		devices = append(devices, &pb.DeviceItem{
			DeviceId:        session.DeviceId,
			DeviceName:      session.DeviceName,
			Platform:        session.Platform,
			AppVersion:      session.AppVersion,
			IsCurrentDevice: deviceID != "" && session.DeviceId == deviceID,
			Status:          int32(session.Status),
			LastSeenAt:      lastSeenAt,
		})
	}

	sort.Slice(devices, func(i, j int) bool {
		if devices[i].LastSeenAt == devices[j].LastSeenAt {
			return devices[i].DeviceId < devices[j].DeviceId
		}
		return devices[i].LastSeenAt > devices[j].LastSeenAt
	})

	return &pb.GetDeviceListResponse{Devices: devices}, nil
}

// KickDevice 踢出设备
func (s *deviceServiceImpl) KickDevice(ctx context.Context, req *pb.KickDeviceRequest) error {
	userUUID := util.GetUserUUIDFromContext(ctx)
	if userUUID == "" {
		logger.Warn(ctx, "踢出设备失败：user_uuid 为空")
		return status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	if req == nil || req.DeviceId == "" {
		return status.Error(codes.InvalidArgument, strconv.Itoa(consts.CodeParamError))
	}

	currentDeviceID := util.GetDeviceIDFromContext(ctx)
	if currentDeviceID != "" && currentDeviceID == req.DeviceId {
		return status.Error(codes.FailedPrecondition, strconv.Itoa(consts.CodeCannotKickCurrent))
	}

	session, err := s.deviceRepo.GetByDeviceID(ctx, userUUID, req.DeviceId)
	if err != nil {
		if errors.Is(err, repository.ErrRecordNotFound) {
			return status.Error(codes.NotFound, strconv.Itoa(consts.CodeDeviceNotFound))
		}
		logger.Error(ctx, "踢出设备失败：查询设备会话失败",
			logger.String("user_uuid", userUUID),
			logger.String("device_id", req.DeviceId),
			logger.ErrorField("error", err),
		)
		return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}
	if session == nil {
		return status.Error(codes.NotFound, strconv.Itoa(consts.CodeDeviceNotFound))
	}

	// 幂等语义：无论 token 是否已删除，都返回成功；仅 Redis 异常才报错。
	if err := s.deviceRepo.DeleteTokens(ctx, userUUID, req.DeviceId); err != nil {
		logger.Error(ctx, "踢出设备失败：删除设备 Token 失败",
			logger.String("user_uuid", userUUID),
			logger.String("device_id", req.DeviceId),
			logger.ErrorField("error", err),
		)
		return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	// status 语义：0=在线, 1=离线, 2=注销, 3=被踢出。
	// 踢设备时：在线/离线 -> 被踢出；注销/已被踢出保持原状态，按幂等成功。
	if session.Status == model.DeviceStatusOnline || session.Status == model.DeviceStatusOffline {
		if err := s.deviceRepo.UpdateOnlineStatus(ctx, userUUID, req.DeviceId, model.DeviceStatusKicked); err != nil {
			if errors.Is(err, repository.ErrRecordNotFound) {
				return status.Error(codes.NotFound, strconv.Itoa(consts.CodeDeviceNotFound))
			}
			logger.Error(ctx, "踢出设备失败：更新设备状态失败",
				logger.String("user_uuid", userUUID),
				logger.String("device_id", req.DeviceId),
				logger.ErrorField("error", err),
			)
			return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
		}
	}

	logger.Info(ctx, "踢出设备成功",
		logger.String("user_uuid", userUUID),
		logger.String("device_id", req.DeviceId),
		logger.Int("before_status", int(session.Status)),
	)

	return nil
}

// GetOnlineStatus 获取用户在线状态
func (s *deviceServiceImpl) GetOnlineStatus(ctx context.Context, req *pb.GetOnlineStatusRequest) (*pb.GetOnlineStatusResponse, error) {
	if req == nil || req.UserUuid == "" {
		return nil, status.Error(codes.InvalidArgument, strconv.Itoa(consts.CodeParamError))
	}

	sessionsByUser, err := s.deviceRepo.BatchGetOnlineStatus(ctx, []string{req.UserUuid})
	if err != nil {
		logger.Error(ctx, "获取在线状态失败：查询设备会话失败",
			logger.String("user_uuid", req.UserUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}
	sessions := sessionsByUser[req.UserUuid]

	// 无设备会话，直接离线返回。
	if len(sessions) == 0 {
		return &pb.GetOnlineStatusResponse{
			Status: &pb.OnlineStatus{
				UserUuid:        req.UserUuid,
				IsOnline:        false,
				LastSeenAt:      0,
				OnlinePlatforms: []string{},
			},
		}, nil
	}

	deviceIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session == nil || session.DeviceId == "" {
			continue
		}
		deviceIDs = append(deviceIDs, session.DeviceId)
	}

	activeTimes, err := s.deviceRepo.GetActiveTimestamps(ctx, req.UserUuid, deviceIDs)
	if err != nil {
		logger.Warn(ctx, "获取在线状态失败：读取设备活跃时间失败，按离线处理",
			logger.String("user_uuid", req.UserUuid),
			logger.ErrorField("error", err),
		)
		activeTimes = map[string]int64{}
	}

	nowSec := time.Now().Unix()
	windowSec := int64(deviceOnlineWindow.Seconds())

	platformSet := make(map[string]struct{})
	isOnline := false
	var lastSeenSec int64

	for _, session := range sessions {
		if session == nil || session.DeviceId == "" {
			continue
		}

		seenSec, ok := activeTimes[session.DeviceId]
		if !ok || seenSec <= 0 {
			continue
		}
		if seenSec > lastSeenSec {
			lastSeenSec = seenSec
		}

		// 在线判定：状态=在线 且 (当前时间 - Redis 活跃时间) <= 窗口。
		if session.Status == model.DeviceStatusOnline && nowSec-seenSec <= windowSec {
			isOnline = true
			if session.Platform != "" {
				platformSet[session.Platform] = struct{}{}
			}
		}
	}

	platforms := make([]string, 0, len(platformSet))
	for p := range platformSet {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)

	return &pb.GetOnlineStatusResponse{
		Status: &pb.OnlineStatus{
			UserUuid:        req.UserUuid,
			IsOnline:        isOnline,
			LastSeenAt:      lastSeenSec * 1000,
			OnlinePlatforms: platforms,
		},
	}, nil
}

// BatchGetOnlineStatus 批量获取在线状态
func (s *deviceServiceImpl) BatchGetOnlineStatus(ctx context.Context, req *pb.BatchGetOnlineStatusRequest) (*pb.BatchGetOnlineStatusResponse, error) {
	if req == nil || len(req.UserUuids) == 0 || len(req.UserUuids) > 100 {
		return nil, status.Error(codes.InvalidArgument, strconv.Itoa(consts.CodeParamError))
	}

	// 去重后查询，返回结果按请求顺序组装。
	unique := make([]string, 0, len(req.UserUuids))
	seen := make(map[string]struct{}, len(req.UserUuids))
	for _, userUUID := range req.UserUuids {
		if userUUID == "" {
			return nil, status.Error(codes.InvalidArgument, strconv.Itoa(consts.CodeParamError))
		}
		if _, ok := seen[userUUID]; ok {
			continue
		}
		seen[userUUID] = struct{}{}
		unique = append(unique, userUUID)
	}

	sessionsByUser, err := s.deviceRepo.BatchGetOnlineStatus(ctx, unique)
	if err != nil {
		logger.Error(ctx, "批量获取在线状态失败：查询设备会话失败",
			logger.Int("user_count", len(unique)),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	nowSec := time.Now().Unix()
	windowSec := int64(deviceOnlineWindow.Seconds())

	users := make([]*pb.OnlineStatusItem, 0, len(req.UserUuids))
	for _, userUUID := range req.UserUuids {
		sessions := sessionsByUser[userUUID]
		if len(sessions) == 0 {
			users = append(users, &pb.OnlineStatusItem{
				UserUuid:   userUUID,
				IsOnline:   false,
				LastSeenAt: 0,
			})
			continue
		}

		deviceIDs := make([]string, 0, len(sessions))
		for _, session := range sessions {
			if session == nil || session.DeviceId == "" {
				continue
			}
			deviceIDs = append(deviceIDs, session.DeviceId)
		}

		activeTimes, err := s.deviceRepo.GetActiveTimestamps(ctx, userUUID, deviceIDs)
		if err != nil {
			logger.Warn(ctx, "批量获取在线状态：读取设备活跃时间失败，按离线处理",
				logger.String("user_uuid", userUUID),
				logger.ErrorField("error", err),
			)
			activeTimes = map[string]int64{}
		}

		isOnline := false
		var lastSeenSec int64
		for _, session := range sessions {
			if session == nil || session.DeviceId == "" {
				continue
			}
			seenSec, ok := activeTimes[session.DeviceId]
			if !ok || seenSec <= 0 {
				continue
			}
			if seenSec > lastSeenSec {
				lastSeenSec = seenSec
			}
			if session.Status == model.DeviceStatusOnline && nowSec-seenSec <= windowSec {
				isOnline = true
			}
		}

		users = append(users, &pb.OnlineStatusItem{
			UserUuid:   userUUID,
			IsOnline:   isOnline,
			LastSeenAt: lastSeenSec * 1000,
		})
	}

	return &pb.BatchGetOnlineStatusResponse{
		Users: users,
	}, nil
}
