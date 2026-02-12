package grpcx

import (
	"context"
	"time"

	"ChatServer/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// LoggingConfig 日志拦截器配置。
type LoggingConfig struct {
	// SlowThreshold 慢请求阈值，超过此值的请求日志级别从 Info 升为 Warn。
	// 零值表示禁用慢请求检测（所有正常请求都记 Info）。
	SlowThreshold time.Duration
	// IgnoreMethods 不记录日志的方法全路径列表（如健康检查）。
	// 示例：[]string{"/grpc.health.v1.Health/Check"}
	IgnoreMethods []string
}

// DefaultLoggingConfig 返回默认日志配置：500ms 慢请求阈值，忽略健康检查方法。
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		SlowThreshold: 500 * time.Millisecond,
		IgnoreMethods: []string{"/grpc.health.v1.Health/Check"},
	}
}

// LoggingUnaryInterceptor 记录每次 Unary 请求的 method、耗时、状态码。
// 错误请求始终记 Warn；正常请求根据 SlowThreshold 决定 Info 或 Warn。
func LoggingUnaryInterceptor(cfgs ...LoggingConfig) grpc.UnaryServerInterceptor {
	cfg := DefaultLoggingConfig()
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}

	ignoreSet := make(map[string]struct{}, len(cfg.IgnoreMethods))
	for _, m := range cfg.IgnoreMethods {
		ignoreSet[m] = struct{}{}
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		// 跳过忽略列表中的方法
		if _, skip := ignoreSet[info.FullMethod]; skip {
			return handler(ctx, req)
		}

		start := time.Now()
		resp, err = handler(ctx, req)
		cost := time.Since(start)
		code := status.Code(err)

		if err != nil {
			logger.Warn(ctx, "grpc unary request",
				logger.String("method", info.FullMethod),
				logger.Duration("cost", cost),
				logger.String("code", code.String()),
				logger.ErrorField("error", err),
			)
		} else if cfg.SlowThreshold > 0 && cost >= cfg.SlowThreshold {
			logger.Warn(ctx, "grpc unary slow request",
				logger.String("method", info.FullMethod),
				logger.Duration("cost", cost),
				logger.String("code", code.String()),
			)
		} else {
			logger.Info(ctx, "grpc unary request",
				logger.String("method", info.FullMethod),
				logger.Duration("cost", cost),
				logger.String("code", code.String()),
			)
		}

		return resp, err
	}
}
