package main

import (
	"ChatServer/apps/connect/internal/grpc"
	"ChatServer/apps/connect/internal/handler"
	"ChatServer/apps/connect/internal/manager"
	"ChatServer/apps/connect/internal/server"
	"ChatServer/apps/connect/internal/svc"
	userpb "ChatServer/apps/user/pb"
	"ChatServer/config"
	"ChatServer/pkg/ctxmeta"
	"ChatServer/pkg/deviceactive"
	"ChatServer/pkg/logger"
	pkgredis "ChatServer/pkg/redis"
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// 初始化根上下文，并放入一个默认 trace_id。
	// connect 服务不是从 HTTP 请求起步，因此先放一个固定值用于启动期日志串联。
	ctx := ctxmeta.WithTraceID(context.Background(), "0")

	// 1) 初始化日志组件（必须最先完成，后续模块初始化都依赖日志输出）。
	logCfg := config.DefaultLoggerConfig()
	l, err := logger.Build(logCfg)
	if err != nil {
		panic(err)
	}
	logger.ReplaceGlobal(l)
	defer func() {
		_ = l.Sync()
	}()

	// 2) 初始化 Redis。
	// 说明：
	// - connect 的鉴权兜底依赖 Redis。
	// - 这里采用降级策略：Redis 不可用时服务仍可启动（仅能力受限）。
	redisCfg := config.DefaultRedisConfig()
	redisClient, err := pkgredis.Build(redisCfg)
	if err != nil {
		logger.Warn(ctx, "Connect 服务 Redis 初始化失败，降级为无 Redis 模式",
			logger.ErrorField("error", err),
		)
		redisClient = nil
	} else {
		pkgredis.ReplaceGlobal(redisClient)
		logger.Info(ctx, "Connect 服务 Redis 初始化成功",
			logger.String("addr", redisCfg.Addr),
		)
	}

	// 3) 初始化 user-service gRPC 客户端。
	// 用于连接建立/断开时通知 user-service 更新设备在线状态。
	// 降级策略：连接失败时 connect 服务照常启动，仅跳过设备状态 RPC。
	userGRPCAddr := os.Getenv("USER_GRPC_ADDR")
	if userGRPCAddr == "" {
		userGRPCAddr = ":9090"
	}
	var userDeviceClient userpb.DeviceServiceClient
	var userGRPCConn *googlegrpc.ClientConn
	userGRPCConn, err = googlegrpc.NewClient(
		userGRPCAddr,
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		logger.Warn(ctx, "user-service gRPC 连接创建失败，降级为无设备状态同步模式",
			logger.String("addr", userGRPCAddr),
			logger.ErrorField("error", err),
		)
	} else {
		userDeviceClient = userpb.NewDeviceServiceClient(userGRPCConn)
		logger.Info(ctx, "user-service gRPC 客户端初始化成功",
			logger.String("addr", userGRPCAddr),
		)
	}

	// 3.5) 初始化设备活跃时间同步器（分片节流 map + 缓冲 map + 后台批量消费）。
	deviceActiveCfg := config.DefaultDeviceActiveConfig()
	var activeSyncer *deviceactive.Syncer
	if userDeviceClient != nil {
		activeSyncer, err = deviceactive.NewSyncer(deviceactive.Config{
			ShardCount:     deviceActiveCfg.ShardCount,
			UpdateInterval: deviceActiveCfg.UpdateInterval,
			FlushInterval:  deviceActiveCfg.FlushInterval,
			WorkerCount:    deviceActiveCfg.WorkerCount,
			QueueSize:      deviceActiveCfg.QueueSize,
			BatchHandler: func(_ context.Context, items []deviceactive.BatchItem) error {
				var firstErr error
				for _, item := range items {
					rpcCtx, cancel := context.WithTimeout(context.Background(), deviceActiveCfg.RPCTimeout)
					_, callErr := userDeviceClient.UpdateDeviceStatus(rpcCtx, &userpb.UpdateDeviceStatusRequest{
						UserUuid: item.UserUUID,
						DeviceId: item.DeviceID,
						Status:   int32(0), // 在线：用于触发 user 侧活跃时间更新
					})
					cancel()
					if callErr != nil && firstErr == nil {
						firstErr = callErr
					}
				}
				return firstErr
			},
		})
		if err != nil {
			logger.Warn(ctx, "Connect 设备活跃同步器初始化失败，降级为无活跃时间同步",
				logger.ErrorField("error", err),
			)
			activeSyncer = nil
		} else {
			logger.Info(ctx, "Connect 设备活跃同步器初始化完成",
				logger.Int("shard_count", deviceActiveCfg.ShardCount),
				logger.Duration("update_interval", deviceActiveCfg.UpdateInterval),
				logger.Duration("flush_interval", deviceActiveCfg.FlushInterval),
				logger.Int("worker_count", deviceActiveCfg.WorkerCount),
			)
		}
	}

	// 4) 组装核心依赖：
	// - manager: 连接注册/注销与在线连接索引。
	// - svc:     connect 业务逻辑（鉴权、心跳、活跃时间、设备状态）。
	// - handler: Gin /ws 入口，承接协议层逻辑。
	connManager := manager.NewConnectionManager()
	connectSvc := svc.NewConnectService(redisClient, userDeviceClient, activeSyncer)
	wsHandler := handler.NewWSHandler(connManager, connectSvc)

	// 5) 构建 HTTP 服务（包含 /health、/metrics 与 /ws）。
	srvCfg := server.DefaultConfig()
	srv := server.New(srvCfg, wsHandler, connManager)

	// 6) 构建 gRPC 服务。
	// gRPC 监听独立端口，提供 PushToDevice/PushToUser/BroadcastToUsers/
	// KickConnection/GetOnlineStatus/BatchGetOnlineStatus。
	grpcAddr := os.Getenv("CONNECT_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9091"
	}
	grpcSrv := grpc.NewServer(grpcAddr, connManager)

	// 7) 后台启动 HTTP 监听。
	// ListenAndServe 的正常退出会返回 http.ErrServerClosed，这种情况不视为启动失败。
	go func() {
		logger.Info(ctx, "Connect HTTP 服务启动中",
			logger.String("addr", srvCfg.Addr),
		)
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error(ctx, "Connect HTTP 服务启动失败",
				logger.ErrorField("error", err),
			)
		}
	}()

	// 8) 后台启动 gRPC 监听。
	go func() {
		logger.Info(ctx, "Connect gRPC 服务启动中",
			logger.String("addr", grpcAddr),
		)
		if err := grpcSrv.Start(); err != nil {
			logger.Error(ctx, "Connect gRPC 服务启动失败",
				logger.ErrorField("error", err),
			)
		}
	}()

	// 9) 阻塞等待系统退出信号（Ctrl+C / SIGTERM）。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 10) 优雅关闭流程：
	// - 先停 gRPC（不再接受新的 RPC 调用）。
	// - 再关闭连接管理器，主动断开所有 WebSocket 连接，避免悬挂连接。
	// - 关闭 user-service gRPC 连接。
	// - 最后关闭 HTTP 服务，等待进行中的请求在超时时间内结束。
	logger.Info(ctx, "Connect 服务开始优雅停机")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	grpcSrv.Stop()
	connManager.Shutdown()
	connectSvc.ShutdownStatusWorkers()
	if userGRPCConn != nil {
		if closeErr := userGRPCConn.Close(); closeErr != nil {
			logger.Warn(ctx, "关闭 user-service gRPC 连接失败",
				logger.ErrorField("error", closeErr),
			)
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(ctx, "Connect 服务优雅停机失败",
			logger.ErrorField("error", err),
		)
		return
	}

	logger.Info(ctx, "Connect 服务已退出")
}
