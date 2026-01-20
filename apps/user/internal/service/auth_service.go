package service

import (
	"ChatServer/apps/user/internal/dto"
	"ChatServer/apps/user/internal/repository"
	"ChatServer/apps/user/internal/utils"
	"ChatServer/consts"
	"ChatServer/pkg/logger"
	"context"
	"errors"
	"strconv"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// authServiceImpl 认证服务实现
type authServiceImpl struct {
	authRepo   repository.IAuthRepository
	deviceRepo repository.IDeviceRepository
}

// NewAuthService 创建认证服务实例
func NewAuthService(
	authRepo repository.IAuthRepository,
	deviceRepo repository.IDeviceRepository,
) AuthService {
	return &authServiceImpl{
		authRepo:   authRepo,
		deviceRepo: deviceRepo,
	}
}

// Register 用户注册
func (s *authServiceImpl) Register(ctx context.Context, req *dto.RegisterRequest) (*dto.RegisterResponse, error) {
	return nil, status.Error(codes.Unimplemented, "注册功能暂未实现")
}

// Login 用户登录（密码）
// 业务流程：
//  1. 根据账号（手机号或邮箱）查询用户
//  2. 校验用户状态（是否被禁用）
//  3. 校验密码
//  4. 返回用户信息（供Gateway生成Token）
//
// 错误码映射：
//   - codes.NotFound: 用户不存在
//   - codes.Unauthenticated: 密码错误
//   - codes.PermissionDenied: 用户被禁用
//   - codes.Internal: 系统内部错误
func (s *authServiceImpl) Login(ctx context.Context, req *dto.LoginRequest) (*dto.LoginResponse, error) {
	// 记录登录请求（账号脱敏）
	logger.Info(ctx, "用户登录请求",
		logger.String("account", utils.MaskPhone(req.Account)),
		logger.String("device_name", req.DeviceInfo.GetDeviceName()),
		logger.String("platform", req.DeviceInfo.GetPlatform()),
	)

	// 1. 根据账号查询用户（先尝试手机号，再尝试邮箱）
	user, err := s.authRepo.GetByPhone(ctx, req.Account)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 尝试通过邮箱查询
			user, err = s.authRepo.GetByEmail(ctx, req.Account)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					logger.Warn(ctx, "用户不存在",
						logger.String("account", utils.MaskPhone(req.Account)),
					)
					return nil, status.Error(codes.NotFound, strconv.Itoa(consts.CodeUserNotFound))
				}
				logger.Error(ctx, "查询用户失败",
					logger.String("account", utils.MaskPhone(req.Account)),
					logger.ErrorField("error", err),
				)
				return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
			}
		} else {
			logger.Error(ctx, "查询用户失败",
				logger.String("account", utils.MaskPhone(req.Account)),
				logger.ErrorField("error", err),
			)
			return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
		}
	}

	// 2. 校验用户状态
	if user.Status == 1 {
		logger.Warn(ctx, "用户已被禁用",
			logger.String("user_uuid", user.Uuid),
			logger.String("account", utils.MaskPhone(req.Account)),
		)
		return nil, status.Error(codes.PermissionDenied, strconv.Itoa(consts.CodeUserDisabled))
	}

	// 3. 校验密码
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
	if err != nil {
		logger.Warn(ctx, "密码错误",
			logger.String("user_uuid", user.Uuid),
			logger.String("account", utils.MaskPhone(req.Account)),
		)
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodePasswordError))
	}

	// 4. 登录成功
	logger.Info(ctx, "用户登录成功",
		logger.String("user_uuid", user.Uuid),
		logger.String("account", utils.MaskPhone(req.Account)),
		logger.String("device_id", req.DeviceInfo.GetDeviceName()),
	)

	// 返回用户信息
	return &dto.LoginResponse{
		UserInfo: dto.ConvertModelToUserInfo(user),
	}, nil
}

// LoginByCode 验证码登录
func (s *authServiceImpl) LoginByCode(ctx context.Context, req *dto.LoginByCodeRequest) (*dto.LoginByCodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "验证码登录功能暂未实现")
}

// SendVerifyCode 发送验证码
func (s *authServiceImpl) SendVerifyCode(ctx context.Context, req *dto.SendVerifyCodeRequest) (*dto.SendVerifyCodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "发送验证码功能暂未实现")
}

// VerifyCode 校验验证码
func (s *authServiceImpl) VerifyCode(ctx context.Context, req *dto.VerifyCodeRequest) (*dto.VerifyCodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "校验验证码功能暂未实现")
}

// RefreshToken 刷新Token
func (s *authServiceImpl) RefreshToken(ctx context.Context, req *dto.RefreshTokenRequest) (*dto.RefreshTokenResponse, error) {
	return nil, status.Error(codes.Unimplemented, "刷新Token功能暂未实现")
}

// Logout 用户登出
func (s *authServiceImpl) Logout(ctx context.Context, req *dto.LogoutRequest) error {
	return status.Error(codes.Unimplemented, "登出功能暂未实现")
}

// ResetPassword 重置密码
func (s *authServiceImpl) ResetPassword(ctx context.Context, req *dto.ResetPasswordRequest) error {
	return status.Error(codes.Unimplemented, "重置密码功能暂未实现")
}
