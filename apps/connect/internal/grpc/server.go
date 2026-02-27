package grpc

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/013677890/LCchat-Backend/apps/connect/internal/manager"
	"github.com/013677890/LCchat-Backend/apps/connect/pb"
	"github.com/013677890/LCchat-Backend/pkg/grpcx"
	"github.com/013677890/LCchat-Backend/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
)

// Server 封装 connect gRPC 服务的启动与停机。
type Server struct {
	pb.UnimplementedConnectServiceServer
	grpcServer  *grpc.Server
	connManager *manager.ConnectionManager
	addr        string
}

// NewServer 创建 connect gRPC Server。
// addr 示例：":9091"。
func NewServer(addr string, connManager *manager.ConnectionManager) *Server {
	s := &Server{
		connManager: connManager,
		addr:        addr,
	}

	// 构建拦截器链：Recovery → Metadata → RateLimit → Metrics → Logging
	// connect 的 RPS 阈值高于 user 服务（大量推送调用）。
	rateLimitCfg := grpcx.RateLimitConfig{
		RequestsPerSecond: 5000,
		Burst:             8000,
	}
	metrics := grpcx.NewMetrics(grpcx.MetricsConfig{Namespace: "connect"})

	unaryInters := []grpc.UnaryServerInterceptor{
		grpcx.RecoveryUnaryInterceptor(),
		grpcx.MetadataUnaryInterceptor(),
		grpcx.RateLimitUnaryInterceptor(rateLimitCfg),
		metrics.UnaryInterceptor(),
		grpcx.LoggingUnaryInterceptor(grpcx.LoggingConfig{
			SlowThreshold: 200 * time.Millisecond, // 推送类 RPC 要求更低延迟
			IgnoreMethods: []string{"/grpc.health.v1.Health/Check"},
		}),
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(unaryInters...),
	)
	pb.RegisterConnectServiceServer(grpcServer, s)

	// 开发/调试阶段开启反射，方便 grpcurl 等工具调用。
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" || ginMode == "debug" {
		reflection.Register(grpcServer)
	}

	s.grpcServer = grpcServer
	return s
}

// Start 启动 gRPC 监听，阻塞直到服务关闭。
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	return s.grpcServer.Serve(lis)
}

// Stop 优雅停机。
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}

// ==================== RPC 实现 ====================

// PushToDevice 向指定用户的指定设备投递消息。
func (s *Server) PushToDevice(ctx context.Context, req *pb.PushToDeviceRequest) (*pb.PushToDeviceResponse, error) {
	data, err := proto.Marshal(req.Message)
	if err != nil {
		logger.Warn(ctx, "PushToDevice: 序列化 MessageEnvelope 失败",
			logger.ErrorField("error", err),
		)
		return &pb.PushToDeviceResponse{Delivered: false}, nil
	}

	delivered := s.connManager.SendToDevice(req.UserUuid, req.DeviceId, data)
	return &pb.PushToDeviceResponse{Delivered: delivered}, nil
}

// PushToUser 向用户所有在线设备广播。
func (s *Server) PushToUser(ctx context.Context, req *pb.PushToUserRequest) (*pb.PushToUserResponse, error) {
	data, err := proto.Marshal(req.Message)
	if err != nil {
		logger.Warn(ctx, "PushToUser: 序列化 MessageEnvelope 失败",
			logger.ErrorField("error", err),
		)
		return &pb.PushToUserResponse{DeliveredCount: 0}, nil
	}

	count := s.connManager.SendToUser(req.UserUuid, data)
	return &pb.PushToUserResponse{DeliveredCount: int32(count)}, nil
}

// BroadcastToUsers 批量向多个用户广播相同的消息。
func (s *Server) BroadcastToUsers(ctx context.Context, req *pb.BroadcastToUsersRequest) (*pb.BroadcastToUsersResponse, error) {
	data, err := proto.Marshal(req.Message)
	if err != nil {
		logger.Warn(ctx, "BroadcastToUsers: 序列化 MessageEnvelope 失败",
			logger.ErrorField("error", err),
		)
		return &pb.BroadcastToUsersResponse{}, nil
	}

	var successCount, totalDelivered int32
	for _, userUUID := range req.UserUuids {
		count := s.connManager.SendToUser(userUUID, data)
		if count > 0 {
			successCount++
			totalDelivered += int32(count)
		}
	}

	return &pb.BroadcastToUsersResponse{
		SuccessCount:   successCount,
		TotalDelivered: totalDelivered,
	}, nil
}

// KickConnection 主动断开指定设备连接。
func (s *Server) KickConnection(ctx context.Context, req *pb.KickConnectionRequest) (*pb.KickConnectionResponse, error) {
	success := s.connManager.KickDevice(req.UserUuid, req.DeviceId)

	if success {
		logger.Info(ctx, "KickConnection: 连接已断开",
			logger.String("user_uuid", req.UserUuid),
			logger.String("device_id", req.DeviceId),
			logger.String("reason", req.Reason),
		)
	}

	return &pb.KickConnectionResponse{Success: success}, nil
}
