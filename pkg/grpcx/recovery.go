package grpcx

import (
	"context"

	"ChatServer/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RecoveryUnaryInterceptor 捕获 handler 内的 panic，避免单个请求的异常崩溃整个进程。
// 捕获后记录 Error 日志（含 method + panic 值），并返回 codes.Internal。
func RecoveryUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error(ctx, "panic recovered in grpc handler",
					logger.Any("panic", r),
					logger.String("method", info.FullMethod),
				)
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}
