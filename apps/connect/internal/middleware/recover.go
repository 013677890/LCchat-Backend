package middleware

import (
	"ChatServer/consts"
	"ChatServer/pkg/ctxmeta"
	"ChatServer/pkg/logger"
	"ChatServer/pkg/result"
	"errors"
	"net"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
)

// RecoverMiddleware 捕获握手阶段 panic，避免进程崩溃。
// stack=true 时会输出堆栈，便于排查线上问题。
func RecoverMiddleware(stack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				ctx := ctxmeta.BuildContextFromGin(c)

				// 识别客户端提前断连场景，避免污染错误日志。
				var brokenPipe bool
				if ne, ok := recovered.(*net.OpError); ok {
					var se *os.SyscallError
					if errors.As(ne.Err, &se) {
						errText := strings.ToLower(se.Error())
						if strings.Contains(errText, "broken pipe") || strings.Contains(errText, "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				if brokenPipe {
					logger.Warn(ctx, "客户端在握手阶段提前断开连接",
						logger.Any("error", recovered),
						logger.String("method", c.Request.Method),
						logger.String("path", c.Request.URL.Path),
						logger.String("ip", c.ClientIP()),
					)
					c.Abort()
					return
				}

				httpRequest, _ := httputil.DumpRequest(c.Request, false)
				if stack {
					logger.Error(ctx, "Connect 服务捕获到 panic",
						logger.Any("error", recovered),
						logger.String("method", c.Request.Method),
						logger.String("path", c.Request.URL.Path),
						logger.String("query", c.Request.URL.RawQuery),
						logger.String("ip", c.ClientIP()),
						logger.String("user-agent", c.Request.UserAgent()),
						logger.String("request", string(httpRequest)),
						logger.String("stack", string(debug.Stack())),
					)
				} else {
					logger.Error(ctx, "Connect 服务捕获到 panic",
						logger.Any("error", recovered),
						logger.String("method", c.Request.Method),
						logger.String("path", c.Request.URL.Path),
						logger.String("query", c.Request.URL.RawQuery),
						logger.String("ip", c.ClientIP()),
						logger.String("user-agent", c.Request.UserAgent()),
						logger.String("request", string(httpRequest)),
					)
				}

				result.Fail(c, nil, consts.CodeInternalError)
				c.Abort()
			}
		}()
		c.Next()
	}
}
