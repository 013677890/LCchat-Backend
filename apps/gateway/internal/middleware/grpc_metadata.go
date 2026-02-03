package middleware

import (
	"ChatServer/pkg/util"
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// GRPCMetadataInterceptor 将上下文信息注入 gRPC metadata（用于透传 trace/user/device/ip）
func GRPCMetadataInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		if traceID, ok := ctx.Value("trace_id").(string); ok && traceID != "" {
			md.Set("trace_id", traceID)
		}
		if userUUID, ok := ctx.Value(util.ContextKeyUserUUID).(string); ok && userUUID != "" {
			md.Set("user_uuid", userUUID)
		}
		if deviceID, ok := ctx.Value(util.ContextKeyDeviceID).(string); ok && deviceID != "" {
			md.Set("device_id", deviceID)
		}
		if clientIP, ok := ctx.Value(util.ContextKeyClientIP).(string); ok && clientIP != "" {
			md.Set("x-real-ip", clientIP)
			md.Set("client_ip", clientIP)
		}

		ctx = metadata.NewOutgoingContext(ctx, md)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
