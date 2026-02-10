package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig 定义跨域策略。
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     string
	AllowHeaders     string
	ExposeHeaders    string
	AllowCredentials bool
}

// DefaultCORSConfig 返回 connect 的默认跨域配置。
// 可通过环境变量 CONNECT_CORS_ALLOW_ORIGINS 传入逗号分隔白名单。
// 未配置时默认允许所有来源（动态回显 Origin，兼容携带凭据场景）。
func DefaultCORSConfig() CORSConfig {
	origins := parseCSV(os.Getenv("CONNECT_CORS_ALLOW_ORIGINS"))
	if len(origins) == 0 {
		origins = []string{"*"}
	}

	return CORSConfig{
		AllowOrigins:     origins,
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Authorization,Content-Type,X-Requested-With,X-Device-ID,X-Request-ID",
		ExposeHeaders:    "X-Request-ID",
		AllowCredentials: true,
	}
}

// CORSMiddleware 处理跨域响应头。
// 说明：WebSocket 握手是 HTTP 请求阶段，浏览器端仍会带 Origin，
// 这里统一在 connect 服务层做跨域策略输出。
func CORSMiddleware(cfg CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && isOriginAllowed(origin, cfg.AllowOrigins) {
			// 当允许凭据时不能返回 "*"，因此统一回显请求来源。
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")

			if cfg.AllowCredentials {
				c.Header("Access-Control-Allow-Credentials", "true")
			}
			if cfg.AllowHeaders != "" {
				c.Header("Access-Control-Allow-Headers", cfg.AllowHeaders)
			}
			if cfg.AllowMethods != "" {
				c.Header("Access-Control-Allow-Methods", cfg.AllowMethods)
			}
			if cfg.ExposeHeaders != "" {
				c.Header("Access-Control-Expose-Headers", cfg.ExposeHeaders)
			}
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func isOriginAllowed(origin string, allowList []string) bool {
	if origin == "" || len(allowList) == 0 {
		return false
	}
	for _, allowed := range allowList {
		v := strings.TrimSpace(allowed)
		if v == "*" || strings.EqualFold(v, origin) {
			return true
		}
	}
	return false
}

func parseCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
