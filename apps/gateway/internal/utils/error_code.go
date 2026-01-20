package utils

import (
	"google.golang.org/grpc/status"
	"ChatServer/consts"
)

// 从grpc错误中提取业务错误码
func ExtractErrorCode(err error) int {
	if err == nil {
		return 0
	}
	st, ok := status.FromError(err)
	if !ok {
		return consts.CodeInternalError
	}
	return int(st.Code())
}