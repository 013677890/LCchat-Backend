// Package server 保留用户服务的 gRPC 启动入口。
// 拦截器和通用逻辑已迁移到 pkg/grpcx，此文件仅保留向后兼容的类型别名和转发函数，
// 后续可直接在 main.go 中使用 grpcx.Start()，不再需要此文件。
package server

import (
	"ChatServer/pkg/grpcx"
	"context"

	"google.golang.org/grpc"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

// Options 向后兼容别名，建议直接使用 grpcx.ServerOptions。
type Options = grpcx.ServerOptions

// Start 向后兼容转发，建议直接使用 grpcx.Start()。
func Start(ctx context.Context, opts Options, register func(s *grpc.Server, health healthgrpc.HealthServer)) error {
	_, err := grpcx.Start(ctx, opts, register)
	return err
}

// NewHealthServer 向后兼容的健康检查创建函数。
// 在使用 grpcx.Start + EnableHealth=true 时，无需手动调用此函数。
func NewHealthServer() healthgrpc.HealthServer {
	// grpcx 内部已经使用 newHealthServer()，这里留作兼容。
	return nil
}
