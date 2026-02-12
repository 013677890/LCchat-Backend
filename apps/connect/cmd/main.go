package main

import (
	"ChatServer/apps/connect/internal/grpc"
	"ChatServer/apps/connect/internal/handler"
	"ChatServer/apps/connect/internal/manager"
	"ChatServer/apps/connect/internal/server"
	"ChatServer/apps/connect/internal/svc"
	"ChatServer/config"
	"ChatServer/pkg/ctxmeta"
	"ChatServer/pkg/logger"
	pkgredis "ChatServer/pkg/redis"
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
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
	// - connect 的鉴权兜底与设备活跃时间写入都依赖 Redis。
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

	// 3) 组装核心依赖：
	// - manager: 连接注册/注销与在线连接索引。
	// - svc:     connect 业务逻辑（鉴权、心跳、活跃时间）。
	// - handler: Gin /ws 入口，承接协议层逻辑。
	connManager := manager.NewConnectionManager()
	connectSvc := svc.NewConnectService(redisClient)
	wsHandler := handler.NewWSHandler(connManager, connectSvc)

	// 4) 构建 HTTP 服务（包含 /health、/metrics 与 /ws）。
	srvCfg := server.DefaultConfig()
	srv := server.New(srvCfg, wsHandler, connManager)

	// 5) 构建 gRPC 服务。
	// gRPC 监听独立端口，提供 PushToDevice/PushToUser/BroadcastToUsers/
	// KickConnection/GetOnlineStatus/BatchGetOnlineStatus。
	grpcAddr := os.Getenv("CONNECT_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9091"
	}
	grpcSrv := grpc.NewServer(grpcAddr, connManager)

	// 6) 后台启动 HTTP 监听。
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

	// 7) 后台启动 gRPC 监听。
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

	// 8) 阻塞等待系统退出信号（Ctrl+C / SIGTERM）。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 9) 优雅关闭流程：
	// - 先停 gRPC（不再接受新的 RPC 调用）。
	// - 再关闭连接管理器，主动断开所有 WebSocket 连接，避免悬挂连接。
	// - 最后关闭 HTTP 服务，等待进行中的请求在超时时间内结束。
	logger.Info(ctx, "Connect 服务开始优雅停机")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	grpcSrv.Stop()
	connManager.Shutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(ctx, "Connect 服务优雅停机失败",
			logger.ErrorField("error", err),
		)
		return
	}

	logger.Info(ctx, "Connect 服务已退出")
}
