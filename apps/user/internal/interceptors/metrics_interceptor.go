package interceptors

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status" // 引入 status 包

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// metricsRequestTotal gRPC 请求总数
	metricsRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_request_total",
			Help: "Total number of gRPC requests",
		},
		[]string{"method", "code"}, // 优化：将 status 改为 code，记录具体状态码
	)

	// metricsRequestDuration gRPC 请求耗时
	metricsRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_request_duration_seconds", // 优化：后缀改为 _seconds
			Help:    "gRPC request latency in seconds",
			// 优化：使用秒级分桶 (5ms 到 5s)
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		},
		[]string{"method"},
	)

	// metricsRequestInFlight 正在处理的请求数
	metricsRequestInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "grpc_request_in_flight",
			Help: "Current number of gRPC requests in flight",
		},
		[]string{"method"},
	)
)

// init 函数：Go 语言特性，包加载时自动执行
// 修复：必须在这里将指标注册到全局注册表，否则 Prometheus 采不到数据
func init() {
	// MustRegister 如果注册失败会 Panic，保证启动时就知道配置错了
	prometheus.MustRegister(metricsRequestTotal)
	prometheus.MustRegister(metricsRequestDuration)
	prometheus.MustRegister(metricsRequestInFlight)
}

// MetricsUnaryInterceptor 监控指标拦截器
func MetricsUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		// 1. 增加正在处理的请求数
		metricsRequestInFlight.WithLabelValues(info.FullMethod).Inc()
		defer metricsRequestInFlight.WithLabelValues(info.FullMethod).Dec()

		// 2. 记录开始时间
		start := time.Now()

		// 3. 执行业务逻辑
		resp, err = handler(ctx, req)

		// 4. 计算耗时（使用秒）
		duration := time.Since(start).Seconds()

		// 5. 记录请求耗时
		metricsRequestDuration.WithLabelValues(info.FullMethod).Observe(duration)

		// 6. 记录请求状态码 (优化点)
		// 使用 status.Code 获取准确的 gRPC 状态码字符串 (如 "OK", "Unavailable")
		code := status.Code(err).String()
		metricsRequestTotal.WithLabelValues(info.FullMethod, code).Inc()

		return resp, err
	}
}

// GetMetricsHandler 获取 metrics handler
func GetMetricsHandler() http.Handler {
	// 使用默认的 Handler，它会从上面的全局注册表里读数据
	return promhttp.Handler()
}