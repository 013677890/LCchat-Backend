package service

import (
	"ChatServer/apps/user/internal/repository"
	pb "ChatServer/apps/user/pb"
	"ChatServer/consts"
	"ChatServer/pkg/logger"
	"ChatServer/pkg/util"
	"context"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// deviceServiceImpl 设备会话服务实现
type deviceServiceImpl struct {
	deviceRepo repository.IDeviceRepository
}

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

	sessions, err := s.deviceRepo.GetByUserUUID(ctx, userUUID)
	if err != nil {
		logger.Error(ctx, "获取设备列表失败",
			logger.String("user_uuid", userUUID),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	devices := make([]*pb.DeviceItem, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		lastSeenAt := util.TimeToUnixMilli(session.UpdatedAt)
		if lastSeenAt == 0 {
			lastSeenAt = util.TimeToUnixMilli(session.CreatedAt)
		}
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

	return &pb.GetDeviceListResponse{Devices: devices}, nil
}

// KickDevice 踢出设备
func (s *deviceServiceImpl) KickDevice(ctx context.Context, req *pb.KickDeviceRequest) error {
	return status.Error(codes.Unimplemented, "踢出设备功能暂未实现")
}

// GetOnlineStatus 获取用户在线状态
func (s *deviceServiceImpl) GetOnlineStatus(ctx context.Context, req *pb.GetOnlineStatusRequest) (*pb.GetOnlineStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取用户在线状态功能暂未实现")
}

// BatchGetOnlineStatus 批量获取在线状态
func (s *deviceServiceImpl) BatchGetOnlineStatus(ctx context.Context, req *pb.BatchGetOnlineStatusRequest) (*pb.BatchGetOnlineStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "批量获取在线状态功能暂未实现")
}
