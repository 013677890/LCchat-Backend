package middleware

import (
	"ChatServer/pkg/ctxmeta"
	"ChatServer/pkg/logger"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// GinLogger 记录 connect 服务的 HTTP 日志。
// 说明：
// - /ws 成功握手会是 101（Switching Protocols）；
// - WebSocket 建连后的消息收发不属于 HTTP 生命周期，不在这里记录。
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		ip := ctxmeta.ClientIPFromGin(c)
		if ip == "" {
			ip = c.ClientIP()
		}

		c.Next()

		status := c.Writer.Status()
		cost := time.Since(start)
		ctx := ctxmeta.BuildContextFromGin(c)

		// /health 正常场景不打日志，避免健康检查刷屏。
		if path == "/health" && status < 500 {
			return
		}

		switch {
		case path == "/ws" && status == http.StatusSwitchingProtocols:
			logger.Info(ctx, "WebSocket 握手成功",
				logger.String("method", method),
				logger.String("path", path),
				logger.String("query", query),
				logger.String("ip", ip),
				logger.Int("status", status),
				logger.Duration("cost", cost),
			)
		case status >= 500:
			logger.Error(ctx, "Connect HTTP 请求失败",
				logger.String("method", method),
				logger.String("path", path),
				logger.String("query", query),
				logger.String("ip", ip),
				logger.Int("status", status),
				logger.Duration("cost", cost),
				logger.String("errors", c.Errors.ByType(gin.ErrorTypeAny).String()),
			)
		case cost > 2*time.Second:
			logger.Warn(ctx, "Connect HTTP 慢请求",
				logger.String("method", method),
				logger.String("path", path),
				logger.String("query", query),
				logger.String("ip", ip),
				logger.Int("status", status),
				logger.Duration("cost", cost),
			)
		}
	}
}
