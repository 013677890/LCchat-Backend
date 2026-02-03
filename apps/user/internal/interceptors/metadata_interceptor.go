package interceptors

import (
	"ChatServer/pkg/util"
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// MetadataUnaryInterceptor 将 gRPC metadata 注入到 context 中
func MetadataUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if traceID := firstValue(md.Get("trace_id")); traceID != "" {
				ctx = context.WithValue(ctx, "trace_id", traceID)
			}
			if userUUID := firstValue(md.Get("user_uuid")); userUUID != "" {
				ctx = context.WithValue(ctx, util.ContextKeyUserUUID, userUUID)
			}
			if deviceID := firstValue(md.Get("device_id")); deviceID != "" {
				ctx = context.WithValue(ctx, util.ContextKeyDeviceID, deviceID)
			}
			clientIP := firstValue(md.Get("x-real-ip"))
			if clientIP == "" {
				clientIP = firstValue(md.Get("x-forwarded-for"))
			}
			if clientIP == "" {
				clientIP = firstValue(md.Get("client_ip"))
			}
			if clientIP != "" {
				ctx = context.WithValue(ctx, util.ContextKeyClientIP, clientIP)
				ctx = context.WithValue(ctx, "ip", clientIP)
			}
		}
		return handler(ctx, req)
	}
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
