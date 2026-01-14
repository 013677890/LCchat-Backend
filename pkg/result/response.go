package result

import (
	"ChatServer/consts" // 你的错误码定义包
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// Response 响应结构体
type Response struct {
	Code    int32       `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
	TraceId string      `json:"trace_id"`
}

var responsePool = &sync.Pool{
	New: func() interface{} {
		return &Response{}
	},
}

// GetResponse 获取响应
func GetResponse() *Response {
	resp := responsePool.Get().(*Response)
	resp.Code = 0
	resp.Message = ""
	resp.Data = nil
	resp.TraceId = ""
	return resp
}

// PutResponse 放回响应
func PutResponse(resp *Response) {
	responsePool.Put(resp)
}

// Result 返回响应
// HTTP 状态码策略：
//   - 业务成功或业务失败（如参数错误、密码错误等）：返回 200，业务状态码在 body 的 code 字段
//   - 系统内部错误（code >= 30000）：返回 500
func Result(c *gin.Context, data interface{}, message string, code int32) {
	traceId := c.GetString("trace_id")
	if message == "" {
		message = consts.GetMessage(code)
	}

	// 判断是否为系统内部错误（3xxxx）
	httpStatus := http.StatusOK
	if code >= 30000 && code < 40000 {
		httpStatus = http.StatusInternalServerError
	}

	// 将业务状态码存储到 context 中供监控中间件使用
	c.Set("business_code", code)

	resp := GetResponse()
	defer PutResponse(resp)
	resp.Code = code
	resp.Message = message
	resp.Data = data
	resp.TraceId = traceId

	c.JSON(httpStatus, resp)

}

// Success 返回成功响应
func Success(c *gin.Context, data interface{}) {
	Result(c, data, "", consts.CodeSuccess)
}

// Fail 返回失败响应
func Fail(c *gin.Context, data interface{}, code int32) {
	Result(c, data, "", code)
}

// SuccessWithMessage 返回成功响应并自定义消息
func SuccessWithMessage(c *gin.Context, data interface{}, message string) {
	Result(c, data, message, consts.CodeSuccess)
}

// FailWithMessage 返回失败响应并自定义消息
func FailWithMessage(c *gin.Context, data interface{}, message string, code int32) {
	Result(c, data, message, code)
}

// SystemError 返回系统错误响应(500)
// 已废弃：建议直接使用 Fail 函数，会自动根据 code 判断返回 200 还是 500
func SystemError(c *gin.Context, code int32) {
	Result(c, nil, "", code)
}
