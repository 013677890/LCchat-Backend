package service

import (
	"ChatServer/apps/user/internal/repository"
	"ChatServer/model"
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// UserService 用户业务服务层
// 职责：
//   - 处理业务逻辑（如密码校验）
//   - 将数据库错误转换为 gRPC 标准错误码
//   - 不直接操作数据库（通过 Repository 层）
type UserService struct {
	userRepo *repository.UserRepository
}

// NewUserService 创建用户服务实例
func NewUserService(userRepo *repository.UserRepository) *UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

// Login 用户登录业务逻辑
// 参数：
//   - ctx: 请求上下文
//   - telephone: 手机号
//   - password: 明文密码（需要与数据库中的 bcrypt 哈希值比对）
//
// 返回：
//   - *model.UserInfo: 用户信息（成功时）
//   - error: gRPC 标准错误
//
// 错误码映射（遵循 gRPC Scheme B）：
//   - codes.NotFound: 用户不存在（数据库 gorm.ErrRecordNotFound）
//   - codes.Unauthenticated: 密码错误（bcrypt 校验失败）
//   - codes.PermissionDenied: 用户被禁用（status=1）
//   - codes.Internal: 系统内部错误（数据库异常、未知错误）
func (s *UserService) Login(ctx context.Context, telephone, password string) (*model.UserInfo, error) {
	// 1. 调用 Repository 层根据手机号查询用户
	user, err := s.userRepo.GetByPhone(ctx, telephone)
	if err != nil {
		// 1.1 判断是否是记录不存在错误
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 用户不存在 -> gRPC NotFound 错误
			return nil, status.Error(codes.NotFound, "用户不存在")
		}
		// 1.2 其他数据库错误 -> gRPC Internal 错误
		return nil, status.Error(codes.Internal, "数据库查询失败")
	}

	// 2. 校验用户状态
	// status=0: 正常，status=1: 禁用
	if user.Status == 1 {
		return nil, status.Error(codes.PermissionDenied, "用户已被禁用")
	}

	// 3. 校验密码（使用 bcrypt 比对）
	// 注意：数据库中存储的是 bcrypt 哈希后的密码（60字符）
	// 传入的 password 是明文密码（如果 Gateway 已经加密，则是加密后的密码）
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		// 密码不匹配 -> gRPC Unauthenticated 错误
		return nil, status.Error(codes.Unauthenticated, "密码错误")
	}

	// 4. 登录成功，返回用户信息
	// 注意：不返回密码字段给调用方
	return user, nil
}


