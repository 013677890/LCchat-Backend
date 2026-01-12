package service

import (
	"ChatServer/apps/gateway/internal/dto"
	"ChatServer/apps/gateway/internal/pb"
	"ChatServer/apps/gateway/internal/utils"
	userpb "ChatServer/apps/user/pb"
	"ChatServer/consts"
	"ChatServer/pkg/logger"
	"context"
	"time"
)

// Login 用户登录服务
// ctx: 请求上下文
// req: 登录请求(包含电话、密码、设备信息)
// deviceId: 设备ID
// 返回: 登录响应和错误
func Login(ctx context.Context, req *dto.LoginRouterToService, deviceId string) (*dto.ServiceLoginResponse, error) {
	startTime := time.Now()

	// 1. 调用用户服务进行身份认证(gRPC)，使用重试机制
	grpcReq := &userpb.LoginRequest{
		Telephone: req.Telephone,
		Password:  req.Password,
	}

	grpcResp, err := pb.Login(ctx, grpcReq)
	if err != nil {
		// gRPC 调用失败，提取业务错误码
		grpcErr := utils.ExtractGRPCError(err)

		// 记录错误日志
		logger.Error(ctx, "调用用户服务 gRPC 失败",
			logger.ErrorField("error", err),
			logger.Int32("business_code", grpcErr.Code),
			logger.String("business_message", grpcErr.Message),
			logger.Duration("duration", time.Since(startTime)),
		)

		// 返回业务错误（不返回 Go error，因为这是预期的业务失败）
		return &dto.ServiceLoginResponse{
			Code:    int(grpcErr.Code),
			Message: "",
			Data:    nil,
		}, nil
	}

	// 2. gRPC 调用成功，检查响应数据
	if grpcResp.UserInfo == nil {
		// 成功返回但 UserInfo 为空，属于非预期的异常情况
		logger.Error(ctx, "gRPC 成功响应但用户信息为空")
		return &dto.ServiceLoginResponse{
			Code:    consts.CodeInternalError,
			Message: "",
			Data:    nil,
		}, nil
	}

	// 3. 令牌生成逻辑
	// 生成 Access Token，用于后续接口请求的身份校验
	accessToken, err := utils.GenerateToken(grpcResp.UserInfo.Uuid, deviceId)
	if err != nil {
		// Token 生成失败通常是内部算法或 JWT 配置问题
		logger.Error(ctx, "生成 Access Token 失败",
			logger.ErrorField("error", err),
		)
		return &dto.ServiceLoginResponse{
			Code:    consts.CodeInternalError,
			Message: "",
			Data:    nil,
		}, nil
	}

	// 生成 Refresh Token，用于 Access Token 过期后的无感刷新
	refreshToken, err := utils.GenerateRefreshToken(grpcResp.UserInfo.Uuid, deviceId)
	if err != nil {
		// Refresh Token 生成失败也视为系统异常
		logger.Error(ctx, "生成 Refresh Token 失败",
			logger.ErrorField("error", err),
		)
		return &dto.ServiceLoginResponse{
			Code:    consts.CodeInternalError,
			Message: "",
			Data:    nil,
		}, nil
	}

	// 4. 构造响应
	loginResponse := &dto.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(utils.AccessExpire / time.Second),
		UserInfo: dto.UserInfo{
			UUID:      grpcResp.UserInfo.Uuid,
			Nickname:  grpcResp.UserInfo.Nickname,
			Telephone: grpcResp.UserInfo.Telephone,
			Email:     grpcResp.UserInfo.Email,
			Avatar:    grpcResp.UserInfo.Avatar,
			Gender:    int8(grpcResp.UserInfo.Gender),
			Signature: grpcResp.UserInfo.Signature,
			Birthday:  grpcResp.UserInfo.Birthday,
		},
	}

	return &dto.ServiceLoginResponse{
		Code:    0,
		Message: "",
		Data:    loginResponse,
	}, nil
}
