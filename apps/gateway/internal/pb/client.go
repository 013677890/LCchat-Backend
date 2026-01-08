package pb

import (
	"context"
	"fmt"
	"time"

	"ChatServer/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	userServiceClient UserServiceClient
	userServiceConn   *grpc.ClientConn
)

// InitUserServiceClient 初始化用户服务gRPC客户端
// address: user服务地址，例如 "localhost:9090"
func InitUserServiceClient(address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 建立gRPC连接
	conn, err := grpc.DialContext(ctx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to user service: %w", err)
	}

	userServiceConn = conn
	userServiceClient = NewUserServiceClient(conn)

	logger.Info(nil, "user service gRPC client initialized",
		logger.String("address", address),
	)

	return nil
}

// CloseUserServiceClient 关闭用户服务gRPC客户端连接
func CloseUserServiceClient() error {
	if userServiceConn != nil {
		return userServiceConn.Close()
	}
	return nil
}

// GetUserServiceClient 获取用户服务客户端
func GetUserServiceClient() UserServiceClient {
	if userServiceClient == nil {
		panic("user service client not initialized. Call InitUserServiceClient first.")
	}
	return userServiceClient
}

// LoginWithRetry 带重试的登录调用
// maxRetries: 最大重试次数
func LoginWithRetry(ctx context.Context, req *LoginRequest, maxRetries int) (*LoginResponse, error) {
	var lastErr error

	for i := 0; i <= maxRetries; i++ {
		if i > 0 {
			logger.Warn(ctx, "retrying login request",
				logger.Int("attempt", i),
				logger.Int("max_retries", maxRetries),
			)
			// 指数退避
			time.Sleep(time.Duration(i) * 100 * time.Millisecond)
		}

		resp, err := GetUserServiceClient().Login(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		logger.Error(ctx, "login request failed",
			logger.Int("attempt", i+1),
			logger.ErrorField("error", err),
		)
	}

	return nil, fmt.Errorf("login failed after %d retries: %w", maxRetries+1, lastErr)
}
