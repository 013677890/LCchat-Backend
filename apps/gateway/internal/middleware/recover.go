package middleware

import (
	"ChatServer/pkg/logger"
	"ChatServer/pkg/result"
	"ChatServer/consts"
	"errors"
	"net"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
)

// GinRecovery recover 项目可能出现的 panic
// stack: 是否打印堆栈信息
func GinRecovery(stack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 1. 获取带 trace_id 的 context
				ctx := NewContextWithGin(c)

				// 2. 判断是否是客户端断开连接（Broken Pipe）
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					var se *os.SyscallError
					if errors.As(ne.Err, &se) {
						errStr := strings.ToLower(se.Error())
						if strings.Contains(errStr, "broken pipe") || strings.Contains(errStr, "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				// 3. 客户端断开连接的情况（非服务端错误）
				if brokenPipe {
					logger.Warn(ctx, "客户端断开连接",
						logger.Any("error", err),
						logger.String("method", c.Request.Method),
						logger.String("path", c.Request.URL.Path),
						logger.String("ip", c.ClientIP()),
					)
					// 连接已断开，无法写入响应，直接中止
					_ = c.Error(err.(error))
					c.Abort()
					return
				}

				// 4. 真正的 Panic（代码 Bug）
				// 获取 HTTP 请求详情
				httpRequest, _ := httputil.DumpRequest(c.Request, false)

				// 记录错误日志（带堆栈信息）
				if stack {
					logger.Error(ctx, "panic recovered",
						logger.Any("error", err),
						logger.String("method", c.Request.Method),
						logger.String("path", c.Request.URL.Path),
						logger.String("query", c.Request.URL.RawQuery),
						logger.String("ip", c.ClientIP()),
						logger.String("user-agent", c.Request.UserAgent()),
						logger.String("request", string(httpRequest)),
						logger.String("stack", string(debug.Stack())),
					)
				} else {
					logger.Error(ctx, "panic recovered",
						logger.Any("error", err),
						logger.String("method", c.Request.Method),
						logger.String("path", c.Request.URL.Path),
						logger.String("query", c.Request.URL.RawQuery),
						logger.String("ip", c.ClientIP()),
						logger.String("user-agent", c.Request.UserAgent()),
						logger.String("request", string(httpRequest)),
					)
				}

				// 返回 500 错误响应
				result.Fail(c, nil, consts.CodeInternalError)
			}
		}()
		c.Next()
	}
}
