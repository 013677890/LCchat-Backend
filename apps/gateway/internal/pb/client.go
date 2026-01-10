package pb

import (
	userpb "ChatServer/apps/user/pb"
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	// userServiceClient 用户服务的gRPC客户端
	userServiceClient userpb.UserServiceClient
	// userServiceConn 用户服务的gRPC连接
	userServiceConn *grpc.ClientConn
)

// gRPC 服务配置，定义重试策略
// 通过 JSON 配置实现自动重试
const retryPolicy = `{
	"methodConfig": [{
		"name": [{"service": "user.UserService"}],
		"waitForReady": true,
		"retryPolicy": {
			"maxAttempts": 5,
			"initialBackoff": "0.1s",
			"maxBackoff": "1s",
			"backoffMultiplier": 2,
			"retryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED", "UNKNOWN"]
		}
	}]
}`

// InitUserServiceClient 初始化用户服务gRPC客户端
// addr: 用户服务地址，格式为 "host:port"，例如 "localhost:9090"
func InitUserServiceClient(addr string) error {

	// 建立gRPC连接
	// 使用 insecure credentials（实际生产环境应该使用TLS）
	// 配置自动重试策略
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(retryPolicy), // 应用重试策略
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(4*1024*1024), // 4MB接收大小
		),
	)
	if err != nil {
		return err
	}

	userServiceConn = conn
	userServiceClient = userpb.NewUserServiceClient(conn)

	return nil
}

// CloseUserServiceClient 关闭用户服务gRPC客户端
func CloseUserServiceClient() error {
	if userServiceConn != nil {
		if err := userServiceConn.Close(); err != nil {
			return err
		}
	}

	return nil
}

// GetUserServiceClient 获取用户服务gRPC客户端
func GetUserServiceClient() userpb.UserServiceClient {
	return userServiceClient
}

// Login 登录方法
// ctx: 上下文
// req: 登录请求
// 返回: 登录响应和错误
// 注意: gRPC 会根据配置的重试策略自动重试失败的请求
func Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	client := GetUserServiceClient()
	if client == nil {
		return nil, fmt.Errorf("user service client not initialized")
	}

	// gRPC 会自动应用重试策略，无需手动重试
	resp, err := client.Login(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
