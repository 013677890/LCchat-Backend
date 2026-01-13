package handler

import (
	"ChatServer/apps/user/internal/service"
	pb "ChatServer/apps/user/pb"
	"ChatServer/model"
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 确保 model 包被使用（编译器检查）
var _ model.UserInfo

// UserServiceHandler gRPC 服务 Handler 层
// 职责：
//   - 解析 gRPC 请求参数
//   - 调用 Service 层执行业务逻辑
//   - 将 Service 层结果转换为 gRPC Response
//   - 不包含任何业务逻辑（业务逻辑在 Service 层）
type UserServiceHandler struct {
	pb.UnimplementedUserServiceServer
	userService *service.UserService
}

// NewUserServiceHandler 创建 Handler 实例
func NewUserServiceHandler(userService *service.UserService) *UserServiceHandler {
	return &UserServiceHandler{
		userService: userService,
	}
}

// Login 用户登录接口实现
// 遵循 gRPC 标准错误处理（Scheme B）：
//   - 成功时返回 (response, nil)
//   - 失败时返回 (nil, status.Error(...))
//   - 不在 Response 中返回 code 或 message 字段
func (h *UserServiceHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	// 1. 参数校验
	if req.Telephone == "" {
		return nil, status.Error(codes.InvalidArgument, "手机号不能为空")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "密码不能为空")
	}

	// 2. 调用 Service 层执行登录业务逻辑
	// Service 层会返回：
	//   - 成功：用户信息和 nil
	//   - 失败：nil 和 gRPC status.Error
	user, err := h.userService.Login(ctx, req.Telephone, req.Password)
	if err != nil {
		// Service 层已经返回了标准的 gRPC 错误，直接透传
		return nil, err
	}

	// 3. 构造 gRPC Response
	// 将数据库模型（model.UserInfo）转换为 Protobuf 消息（pb.UserInfo）
	resp := &pb.LoginResponse{
		UserInfo: &pb.UserInfo{
			Uuid:      user.Uuid,
			Nickname:  user.Nickname,
			Telephone: user.Telephone,
			Email:     user.Email,
			Avatar:    user.Avatar,
			Gender:    int32(user.Gender),
			Signature: user.Signature,
			Birthday:  user.Birthday,
			Status:    int32(user.Status),
			CreatedAt: user.CreatedAt.Unix() * 1000, // 转换为毫秒时间戳
			UpdatedAt: user.UpdatedAt.Unix() * 1000,
		},
	}

	// 4. 返回成功响应
	return resp, nil
}

