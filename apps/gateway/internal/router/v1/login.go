package v1

import (
	"ChatServer/apps/gateway/internal/dto"
	"ChatServer/apps/gateway/internal/middleware"
	"ChatServer/apps/gateway/internal/pb"
	"ChatServer/apps/gateway/internal/utils"
	"ChatServer/pkg/logger"
	"ChatServer/pkg/util"
	"time"

	"github.com/gin-gonic/gin"
)

// Login 用户登录接口
// @Summary 用户登录
// @Description 用户通过手机号和密码登录
// @Tags 认证接口
// @Accept json
// @Produce json
// @Param request body dto.LoginRequest true "登录请求"
// @Success 200 {object} dto.LoginResponse
// @Router /api/v1/public/login [post]
func Login(c *gin.Context) {
	ctx := middleware.NewContextWithGin(c)
	traceId := c.GetString("trace_id")
	ip := c.ClientIP()

	// 1. 绑定请求数据
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn(ctx, "login request bind failed",
			logger.String("trace_id", traceId),
			logger.String("ip", ip),
			logger.ErrorField("error", err),
		)
		c.JSON(400, gin.H{
			"code":    10001,
			"message": "参数验证失败",
			"errors":  []gin.H{{"message": err.Error()}},
		})
		return
	}

	// 2. 记录登录请求(脱敏处理)
	logger.Info(ctx, "login request received",
		logger.String("trace_id", traceId),
		logger.String("ip", ip),
		logger.String("telephone", utils.MaskTelephone(req.Telephone)),
		logger.String("password", utils.MaskPassword(req.Password)),
		logger.String("platform", req.DeviceInfo.Platform),
		logger.String("user_agent", c.Request.UserAgent()),
	)

	// 3. 参数校验
	if len(req.Telephone) != 11 {
		logger.Warn(ctx, "login validation failed: invalid telephone",
			logger.String("trace_id", traceId),
			logger.String("ip", ip),
			logger.String("telephone", utils.MaskTelephone(req.Telephone)),
		)
		c.JSON(400, gin.H{
			"code":    11005,
			"message": "手机号格式错误",
		})
		return
	}

	if len(req.Password) == 0 {
		logger.Warn(ctx, "login validation failed: empty password",
			logger.String("trace_id", traceId),
			logger.String("ip", ip),
		)
		c.JSON(400, gin.H{
			"code":    10001,
			"message": "密码不能为空",
		})
		return
	}

	// 4. 调用用户服务进行认证(gRPC)
	startTime := time.Now()

	grpcReq := &pb.LoginRequest{
		Telephone: req.Telephone,
		Password:  req.Password,
	}

	logger.Debug(ctx, "sending gRPC request to user service",
		logger.String("trace_id", traceId),
		logger.String("telephone", utils.MaskTelephone(req.Telephone)),
	)

	grpcResp, err := pb.LoginWithRetry(ctx, grpcReq, 3)
	duration := time.Since(startTime)

	if err != nil {
		logger.Error(ctx, "gRPC call to user service failed",
			logger.String("trace_id", traceId),
			logger.String("ip", ip),
			logger.String("telephone", utils.MaskTelephone(req.Telephone)),
			logger.ErrorField("error", err),
			logger.Duration("duration", duration),
		)
		c.JSON(500, gin.H{
			"code":    30001,
			"message": "服务暂时不可用",
		})
		return
	}

	logger.Info(ctx, "received gRPC response from user service",
		logger.String("trace_id", traceId),
		logger.Int("code", int(grpcResp.Code)),
		logger.String("message", grpcResp.Message),
		logger.Duration("duration", duration),
	)

	// 5. 处理用户服务返回的响应
	if grpcResp.Code != 0 {
		logger.Warn(ctx, "user authentication failed",
			logger.String("trace_id", traceId),
			logger.String("ip", ip),
			logger.String("telephone", utils.MaskTelephone(req.Telephone)),
			logger.Int("error_code", int(grpcResp.Code)),
			logger.String("error_message", grpcResp.Message),
		)

		c.JSON(400, gin.H{
			"code":    grpcResp.Code,
			"message": grpcResp.Message,
		})
		return
	}

	if grpcResp.UserInfo == nil {
		logger.Error(ctx, "user info is nil in success response",
			logger.String("trace_id", traceId),
		)
		c.JSON(500, gin.H{
			"code":    30001,
			"message": "服务器内部错误",
		})
		return
	}

	logger.Info(ctx, "user authentication successful",
		logger.String("trace_id", traceId),
		logger.String("user_uuid", utils.MaskUUID(grpcResp.UserInfo.Uuid)),
		logger.String("telephone", utils.MaskTelephone(grpcResp.UserInfo.Telephone)),
		logger.String("nickname", grpcResp.UserInfo.Nickname),
		logger.Duration("auth_duration", duration),
	)

	// 6. 生成Token
	// 获取或生成设备ID
	deviceId := c.GetHeader("X-Device-ID")
	if deviceId == "" {
		deviceId = util.NewUUID()
		logger.Debug(ctx, "no device id in header, generated new one",
			logger.String("trace_id", traceId),
			logger.String("device_id", deviceId),
		)
	}

	tokenStartTime := time.Now()
	accessToken, err := utils.GenerateToken(grpcResp.UserInfo.Uuid, deviceId)
	if err != nil {
		logger.Error(ctx, "generate access token failed",
			logger.String("trace_id", traceId),
			logger.String("user_uuid", utils.MaskUUID(grpcResp.UserInfo.Uuid)),
			logger.ErrorField("error", err),
		)
		c.JSON(500, gin.H{
			"code":    30001,
			"message": "服务器内部错误",
		})
		return
	}

	refreshToken, err := utils.GenerateRefreshToken(grpcResp.UserInfo.Uuid, deviceId)
	if err != nil {
		logger.Error(ctx, "generate refresh token failed",
			logger.String("trace_id", traceId),
			logger.String("user_uuid", utils.MaskUUID(grpcResp.UserInfo.Uuid)),
			logger.ErrorField("error", err),
		)
		c.JSON(500, gin.H{
			"code":    30001,
			"message": "服务器内部错误",
		})
		return
	}

	tokenDuration := time.Since(tokenStartTime)
	logger.Info(ctx, "token generated successfully",
		logger.String("trace_id", traceId),
		logger.String("user_uuid", utils.MaskUUID(grpcResp.UserInfo.Uuid)),
		logger.String("device_id", deviceId),
		logger.Duration("token_duration", tokenDuration),
	)

	// 7. 构造响应
	response := dto.LoginResponse{
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

	// 8. 记录登录成功日志
	totalDuration := time.Since(startTime)
	logger.Info(ctx, "login successful",
		logger.String("trace_id", traceId),
		logger.String("ip", ip),
		logger.String("user_uuid", utils.MaskUUID(grpcResp.UserInfo.Uuid)),
		logger.String("telephone", utils.MaskTelephone(grpcResp.UserInfo.Telephone)),
		logger.String("nickname", grpcResp.UserInfo.Nickname),
		logger.String("platform", req.DeviceInfo.Platform),
		logger.Duration("total_duration", totalDuration),
	)

	c.JSON(200, gin.H{
		"code":    0,
		"message": "登录成功",
		"data":    response,
	})
}
