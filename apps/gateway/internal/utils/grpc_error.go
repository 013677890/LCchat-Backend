package utils

import (
	"ChatServer/consts"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCError 封装的 gRPC 错误信息
type GRPCError struct {
	Code    int32  // 业务错误码
	Message string // 错误消息
}

// ExtractGRPCError 从 gRPC error 中提取业务错误码和消息
// 参数：
//   - err: gRPC 返回的错误
//
// 返回：
//   - *GRPCError: 包含业务错误码和消息，如果 err 为 nil 则返回 nil
func ExtractGRPCError(err error) *GRPCError {
	if err == nil {
		return nil
	}

	// 提取 gRPC status
	st, ok := status.FromError(err)
	if !ok {
		// 不是标准的 gRPC 错误，返回内部错误
		return &GRPCError{
			Code:    consts.CodeInternalError,
			Message: consts.GetMessage(consts.CodeInternalError),
		}
	}

	// 尝试从 error details 中获取业务错误码
	// User 服务可以使用 status.WithDetails() 附加业务错误码
	businessCode := grpcCodeToBusinessCode(st.Code())

	// 如果有自定义消息，使用 gRPC 消息；否则使用业务错误码对应的消息
	message := st.Message()
	if message == "" {
		message = consts.GetMessage(businessCode)
	}

	return &GRPCError{
		Code:    businessCode,
		Message: message,
	}
}

// grpcCodeToBusinessCode 将 gRPC status code 映射到业务错误码
// 这个映射规则应该与 User 服务保持一致
func grpcCodeToBusinessCode(code codes.Code) int32 {
	switch code {
	// ========== 客户端错误 ==========
	case codes.InvalidArgument:
		// 参数错误（默认映射，具体错误码由 User 服务在 details 中指定）
		return consts.CodeParamError

	case codes.ResourceExhausted:
		// 资源耗尽（如请求过于频繁）
		return consts.CodeTooManyRequests

	// ========== 认证和权限错误 ==========
	case codes.Unauthenticated:
		// 认证失败（如密码错误）
		// 实际业务中可能是 CodePasswordError (11003)
		// 应该由 User 服务在 details 中明确指定
		return consts.CodePasswordError

	case codes.PermissionDenied:
		// 权限拒绝（如用户被禁用、黑名单等）
		// 默认映射到用户被禁用，具体由 User 服务指定
		return consts.CodeUserDisabled

	// ========== 资源错误 ==========
	case codes.NotFound:
		// 资源不存在（如用户不存在、好友关系不存在等）
		// 默认映射到用户不存在，具体由 User 服务指定
		return consts.CodeUserNotFound

	case codes.AlreadyExists:
		// 资源已存在（如用户已存在、已经是好友等）
		// 默认映射到用户已存在，具体由 User 服务指定
		return consts.CodeUserAlreadyExist

	// ========== 超时错误 ==========
	case codes.DeadlineExceeded:
		// 超时（如验证码过期）
		return consts.CodeVerifyCodeExpire

	// ========== 服务端错误 ==========
	case codes.Internal:
		// 内部错误
		return consts.CodeInternalError

	case codes.Unavailable:
		// 服务不可用
		return consts.CodeServiceUnavailable

	case codes.Unknown:
		// 未知错误
		return consts.CodeInternalError

	// ========== 其他情况 ==========
	default:
		// 默认返回内部错误
		return consts.CodeInternalError
	}
}

// IsGRPCError 判断是否为 gRPC 错误
func IsGRPCError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := status.FromError(err)
	return ok
}

// GetGRPCCode 获取 gRPC status code
func GetGRPCCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	st, ok := status.FromError(err)
	if !ok {
		return codes.Unknown
	}
	return st.Code()
}
