package main

import (
	"ChatServer/apps/gateway/internal/middleware"
	"ChatServer/apps/gateway/internal/pb"
	"ChatServer/apps/gateway/internal/router"
	v1 "ChatServer/apps/gateway/internal/router/v1"
	"ChatServer/apps/gateway/internal/service"
	"ChatServer/config"
	"ChatServer/pkg/async"
	"ChatServer/pkg/logger"
	pkgminio "ChatServer/pkg/minio"
	pkgredis "ChatServer/pkg/redis"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()
	//设置trace_id 为 0
	traceId := "0"
	ctx = context.WithValue(ctx, "trace_id", traceId)

	// 1. 初始化 Redis
	redisCfg := config.DefaultRedisConfig()
	redisClient, err := pkgredis.Build(redisCfg)
	if err != nil {
		logger.Error(ctx, "初始化 Redis 失败",
			logger.ErrorField("error", err),
		)
		// Redis 初始化失败不阻塞启动，但限流功能将降级
		redisClient = nil
	} else {
		pkgredis.ReplaceGlobal(redisClient)
		logger.Info(ctx, "Redis 初始化成功",
			logger.String("addr", redisCfg.Addr),
		)
	}

	// 2. 初始化日志
	cfg := config.DefaultLoggerConfig()
	l, err := logger.Build(cfg)
	if err != nil {
		fmt.Printf("初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	logger.ReplaceGlobal(l)
	defer func() {
		// 同步日志缓冲区
		if err := logger.L().Sync(); err != nil {
			// Sync 在某些情况下会返回错误（如 os.Stdout），可以忽略
			_ = err
		}
	}()

	// 2.5 初始化 Async 协程池
	async.SetContextPropagator(func(parent context.Context) context.Context {
		newCtx := context.Background()
		if parent == nil {
			return newCtx
		}
		if v := parent.Value("trace_id"); v != nil {
			newCtx = context.WithValue(newCtx, "trace_id", v)
		}
		if v := parent.Value("user_uuid"); v != nil {
			newCtx = context.WithValue(newCtx, "user_uuid", v)
		}
		if v := parent.Value("device_id"); v != nil {
			newCtx = context.WithValue(newCtx, "device_id", v)
		}
		if v := parent.Value("ip"); v != nil {
			newCtx = context.WithValue(newCtx, "ip", v)
		}
		return newCtx
	})

	asyncCfg := config.DefaultAsyncConfig()
	if err := async.Init(asyncCfg); err != nil {
		logger.Error(ctx, "初始化 Async 协程池失败", logger.ErrorField("error", err))
		os.Exit(1)
	}
	defer func() {
		if err := async.Release(); err != nil {
			logger.Error(ctx, "释放 Async 协程池失败", logger.ErrorField("error", err))
		}
	}()
	logger.Info(ctx, "Async 协程池初始化完成", logger.Int("pool_size", asyncCfg.PoolSize))

	logger.Info(ctx, "Gateway 服务初始化中...")

	// 3. 初始化 MinIO 对象存储
	minioCfg := config.DefaultMinIOConfig()
	minioClient, err := pkgminio.Build(minioCfg)
	if err != nil {
		logger.Error(ctx, "初始化 MinIO 失败",
			logger.ErrorField("error", err),
		)
		// MinIO 初始化失败不阻塞启动，但文件上传功能将不可用
		minioClient = nil
	} else {
		pkgminio.ReplaceGlobal(minioClient)
		logger.Info(ctx, "MinIO 初始化成功",
			logger.String("endpoint", minioCfg.Endpoint),
			logger.String("bucket", minioCfg.BucketName),
			logger.Bool("use_ssl", minioCfg.UseSSL),
		)
	}

	// 4. 初始化 Redis IP 限流器
	// 参数说明：
	//   - rate: 每秒产生的令牌数 (10.0 表示每秒10个令牌)
	//   - burst: 令牌桶容量 (20 表示桶最多20个令牌)
	//   - redisClient: Redis 客户端实例
	// 示例：10 req/s, burst 20 表示正常情况下每秒10个请求，短时间内最多20个
	middleware.InitRedisRateLimiter(10.0, 20, redisClient)
	logger.Info(ctx, "Redis IP 限流器初始化完成",
		logger.Float64("rate", 10.0),
		logger.Int("burst", 20),
		logger.String("blacklist_key", "gateway:blacklist:ips"),
	)

	// 5. 初始化 gRPC 客户端（依赖注入）
	// TODO: 从配置文件读取user服务地址
	userServiceAddr := "localhost:9090"

	// 5.1 创建熔断器
	userServiceBreaker := pb.CreateCircuitBreaker("user-service")
	logger.Info(ctx, "熔断器创建成功", logger.String("name", "user-service"))

	// 5.2 创建 gRPC 连接
	userServiceConn, err := pb.CreateUserServiceConnection(userServiceAddr, userServiceBreaker)
	if err != nil {
		logger.Error(ctx, "创建用户服务 gRPC 连接失败", logger.ErrorField("error", err))
		os.Exit(1)
	}
	defer func() {
		if err := userServiceConn.Close(); err != nil {
			logger.Error(ctx, "关闭用户服务 gRPC 连接失败", logger.ErrorField("error", err))
		}
	}()
	logger.Info(ctx, "用户服务 gRPC 连接创建成功", logger.String("address", userServiceAddr))

	// 5.3 创建 gRPC 客户端
	userClient := pb.NewUserServiceClient(userServiceConn, userServiceConn, userServiceConn, userServiceConn, userServiceConn, userServiceBreaker)
	logger.Info(ctx, "用户服务 gRPC 客户端初始化完成", logger.String("address", userServiceAddr))

	// 6. 初始化 Service 层（依赖注入）
	authService := service.NewAuthService(userClient)
	logger.Info(ctx, "认证服务初始化完成")

	userService := service.NewUserService(userClient)
	logger.Info(ctx, "用户信息服务初始化完成")

	friendService := service.NewFriendService(userClient)
	logger.Info(ctx, "好友服务初始化完成")

	blacklistService := service.NewBlacklistService(userClient)
	logger.Info(ctx, "黑名单服务初始化完成")

	deviceService := service.NewDeviceService(userClient)
	logger.Info(ctx, "设备服务初始化完成")

	// 7. 初始化 Handler 层（依赖注入）
	authHandler := v1.NewAuthHandler(authService)
	logger.Info(ctx, "认证处理器初始化完成")

	userHandler := v1.NewUserHandler(userService)
	logger.Info(ctx, "用户信息处理器初始化完成")

	friendHandler := v1.NewFriendHandler(friendService)
	logger.Info(ctx, "好友处理器初始化完成")

	blacklistHandler := v1.NewBlacklistHandler(blacklistService)
	logger.Info(ctx, "黑名单处理器初始化完成")

	deviceHandler := v1.NewDeviceHandler(deviceService)
	logger.Info(ctx, "设备处理器初始化完成")

	// 8. 初始化路由（依赖注入）
	// Gin 模式设置: ReleaseMode/DebugMode/TestMode
	gin.SetMode(gin.ReleaseMode)
	r := router.InitRouter(authHandler, userHandler, friendHandler, blacklistHandler, deviceHandler)
	logger.Info(ctx, "路由初始化完成")

	// 9. 配置服务器
	port := 8080 // TODO: 从配置文件读取
	addr := "127.0.0.1:" + fmt.Sprintf("%d", port)

	srv := &http.Server{
		Addr:           addr,
		Handler:        r,
		ReadTimeout:    10 * time.Second, // 读取超时
		WriteTimeout:   10 * time.Second, // 写入超时
		MaxHeaderBytes: 1 << 20,          // 最大请求头 1MB
	}

	// 10. 启动服务器（在 goroutine 中）
	go func() {
		logger.Info(ctx, "Gateway 服务器启动中",
			logger.String("address", addr),
			logger.Int("port", port),
		)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(ctx, "服务器启动失败", logger.ErrorField("error", err))
			os.Exit(1)
		}
	}()

	logger.Info(ctx, "Gateway 服务器启动成功，按 Ctrl+C 关闭")

	// 11. 优雅停机
	quit := make(chan os.Signal, 1)
	// 监听中断信号：Ctrl+C (SIGINT) 和 kill 命令 (SIGTERM)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 阻塞等待信号
	sig := <-quit
	logger.Info(ctx, "收到关闭信号，开始优雅停机...",
		logger.String("signal", sig.String()),
	)

	// 12. 设置超时时间，等待正在处理的请求完成
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 关闭 HTTP 服务器
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(ctx, "服务器强制关闭", logger.ErrorField("error", err))
		os.Exit(1)
	}

	logger.Info(ctx, "Gateway 服务器已优雅退出")
}
