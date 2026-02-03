package server

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ChatServer/apps/user/internal/interceptors"
	"ChatServer/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// Options 定义 gRPC Server 的常用启动参数。
type Options struct {
	Address            string                         // 监听地址，例 :9090
	UnaryInterceptors  []grpc.UnaryServerInterceptor  // 自定义 Unary 拦截器
	StreamInterceptors []grpc.StreamServerInterceptor // 自定义 Stream 拦截器
	MaxRecvMsgSize     int                            // 最大接收包，默认不限制
	MaxSendMsgSize     int                            // 最大发送包，默认不限制
	EnableHealth       bool                           // 是否注册健康检查
	EnableReflection   bool                           // 是否开启反射（建议仅在开发环境）
}

// Start 启动 gRPC Server，负责创建监听、注册服务、处理优雅停机。
// register 由业务方传入，在此回调中完成各服务的 Register。
func Start(ctx context.Context, opts Options, register func(s *grpc.Server, health healthgrpc.HealthServer)) error {
	if opts.Address == "" { //如果地址为空，返回错误
		return status.Error(codes.InvalidArgument, "grpc address is empty")
	}

	grpcOpts := buildServerOptions(opts) //构建grpc.ServerOption
	s := grpc.NewServer(grpcOpts...)

	// 健康检查
	var healthServer healthgrpc.HealthServer
	if opts.EnableHealth {
		//创建健康检查服务
		healthServer = NewHealthServer()
		//注册健康检查服务
		healthgrpc.RegisterHealthServer(s, healthServer)
	}

	// 业务注册
	register(s, healthServer)

	// 反射（仅建议开发/测试开启）
	if opts.EnableReflection {
		reflection.Register(s)
	}

	//监听端口
	lis, err := net.Listen("tcp", opts.Address)
	if err != nil {
		return err
	}

	// 优雅停机：捕获系统信号或 ctx 取消
	go gracefulStop(ctx, s)

	logger.Info(ctx, "gRPC server start", logger.String("addr", opts.Address))
	if err := s.Serve(lis); err != nil { //开始接收请求
		return err
	}
	return nil
}

// buildServerOptions 构建 grpc.ServerOption。
func buildServerOptions(opts Options) []grpc.ServerOption {
	var serverOpts []grpc.ServerOption

	// 消息大小限制
	if opts.MaxRecvMsgSize > 0 {
		serverOpts = append(serverOpts, grpc.MaxRecvMsgSize(opts.MaxRecvMsgSize))
	}
	if opts.MaxSendMsgSize > 0 {
		serverOpts = append(serverOpts, grpc.MaxSendMsgSize(opts.MaxSendMsgSize))
	}

	// 默认拦截器（Recovery + RateLimit + Metrics + Logging）
	// 执行顺序：Recovery(最外层) -> RateLimit -> Metrics -> Logging(最内层）
	unaryInters := []grpc.UnaryServerInterceptor{
		interceptors.RecoveryUnaryInterceptor(),  // 1. panic 恢复
		interceptors.MetadataUnaryInterceptor(),  // 2. 注入 metadata 到 context
		interceptors.RateLimitUnaryInterceptor(), // 3. 全局限流（使用默认配置）
		interceptors.MetricsUnaryInterceptor(),   // 4. 监控指标（QPS、耗时等）
		interceptors.LoggingUnaryInterceptor(),   // 5. 日志记录
	}
	unaryInters = append(unaryInters, opts.UnaryInterceptors...)                // 添加自定义拦截器
	serverOpts = append(serverOpts, grpc.ChainUnaryInterceptor(unaryInters...)) // 构建拦截器链

	if len(opts.StreamInterceptors) > 0 { //添加自定义流拦截器
		serverOpts = append(serverOpts, grpc.ChainStreamInterceptor(opts.StreamInterceptors...))
	}

	return serverOpts
}

// gracefulStop 监听信号或 ctx 取消，执行优雅停机。
func gracefulStop(ctx context.Context, s *grpc.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Warn(ctx, "received signal, graceful stop", logger.String("signal", sig.String()))
	case <-ctx.Done():
		logger.Warn(ctx, "context canceled, graceful stop", logger.Any("err", ctx.Err()))
	}

	// 给正在处理的请求留出时间（GracefulStop 会等待中断）
	stopDone := make(chan struct{})
	go func() {
		s.GracefulStop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		logger.Info(ctx, "grpc server stopped gracefully")
	case <-time.After(10 * time.Second):
		logger.Warn(ctx, "graceful stop timeout, force stop")
		s.Stop()
	}
}

// NewHealthServer 创建健康检查服务，初始状态为 SERVING。
// 业务可在注册服务后自行设置状态。
func NewHealthServer() healthgrpc.HealthServer {
	hs := health.NewServer()
	hs.SetServingStatus("", healthgrpc.HealthCheckResponse_SERVING)
	return hs
}
