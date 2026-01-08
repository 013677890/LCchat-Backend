package middleware

import (
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
		c.Next()

		cost := time.Since(start)
		logger.Info(c.Request.Context(), "gin request",
			logger.Int("status", c.Writer.Status()),
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

// GinRecovery recover 项目可能出现的 panic
func GinRecovery(stack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// TODO: 完善 panic 的日志记录，包括堆栈信息
				logger.Error(c.Request.Context(), "gin panic",
					logger.Any("error", err),
				)
				c.AbortWithStatus(500)
			}
		}()
		c.Next()
	}
}
