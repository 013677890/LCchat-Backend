package router

import (
	"ChatServer/apps/gateway/internal/middleware"
	v1 "ChatServer/apps/gateway/internal/router/v1"
	"ChatServer/pkg/util"

	"github.com/gin-gonic/gin"
)

// InitRouter 初始化路由
func InitRouter() *gin.Engine {
	r := gin.New()

	// 基础中间件 (使用自定义的日志和恢复中间件)
	r.Use(middleware.GinLogger())
	r.Use(middleware.GinRecovery(true))

	// 跨域中间件
	r.Use(middleware.CorsMiddleware())

	// 追踪中间件 (生成 trace_id)
	r.Use(util.TraceLogger())

	// 健康检查（无需认证）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
		})
	})

	// API 路由组
	api := r.Group("/api/v1")
	{
		// 公开接口（不需要认证）
		public := api.Group("/public")
		{
			// 登录接口
			public.POST("/login", v1.Login)
		}

		// 需要认证的接口
		_ = api.Group("/auth")
		//auth.Use(middleware.JWTAuthMiddleware()) // 应用 JWT 认证中间件  测试环境下不启用
		// TODO: 添加需要认证的接口
	}

	return r
}
