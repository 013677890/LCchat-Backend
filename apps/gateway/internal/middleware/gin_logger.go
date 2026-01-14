package middleware

import (
	//"ChatServer/apps/gateway/internal/middleware"
	"ChatServer/pkg/logger"
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// NewContextWithGin 从 gin.Context 创建包含 trace_id 的 context.Context
// 用于将 Gin 上下文中的 trace_id 传递到日志系统
func NewContextWithGin(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	if traceId, exists := c.Get("trace_id"); exists {
		return context.WithValue(ctx, "trace_id", traceId)
	}
	return ctx
}

// GinLogger 接收 gin 框架默认的日志
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		ctx := NewContextWithGin(c)

		logger.Info(ctx, "请求开始",
			logger.String("method", c.Request.Method),
			logger.String("path", path),
			logger.String("query", query),
			logger.String("ip", c.ClientIP()),
			logger.String("user-agent", c.Request.UserAgent()),
		)

		c.Next()

		cost := time.Since(start)
		status := c.Writer.Status()

		// 只记录服务端错误(5xx)和慢请求(>2s),正常请求不记录
		if status >= 500 || cost > 2*time.Second {
			logger.Warn(ctx, "慢请求或服务端错误",
				logger.Int("status", status),
				logger.String("method", c.Request.Method),
				logger.String("path", path),
				logger.String("query", query),
				logger.String("ip", c.ClientIP()),
				logger.String("user-agent", c.Request.UserAgent()),
				logger.String("errors", c.Errors.ByType(gin.ErrorTypePrivate).String()),
				logger.Duration("cost", cost),
			)
		}
	}
}
