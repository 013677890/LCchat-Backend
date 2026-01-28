package middleware

import (
	"ChatServer/pkg/logger"
	pkgredis "ChatServer/pkg/redis"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// ==================== Redis 令牌桶 Lua 脚本 ====================

// luaTokenBucketRedis Redis 令牌桶 Lua 脚本
// 功能：原子性地更新令牌桶并判断是否允许通过
// 参数：
//
//	KEYS[1]: 限流 key (如: rate:limit:ip:{ip})
//	ARGV[1]: 当前时间戳 (毫秒)
//	ARGV[2]: 令牌桶容量
//	ARGV[3]: 每秒产生的令牌数 (乘以1000转换为毫秒精度)
//	ARGV[4]: 每次请求消耗的令牌数
//
// 返回值：
//   - 1: 允许通过
//   - 0: 不允许通过 (令牌不足)
//
// 注意：时间戳使用毫秒级精度以提高计算准确性
const luaTokenBucketRedis = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local rate = tonumber(ARGV[3]) -- 每秒产生的令牌数
local requested = tonumber(ARGV[4])

-- 获取当前状态
local info = redis.call('HMGET', key, 'tokens', 'last_time')
local current_tokens = tonumber(info[1]) or 0
local last_time = tonumber(info[2]) or now

-- 初始化
if current_tokens == nil then
    current_tokens = capacity
    last_time = now
end

-- 计算时间差 (毫秒)
local time_diff = math.max(0, now - last_time)

-- 计算补充令牌: (时间差ms * 速率) / 1000
-- 比如: 100ms * 10r/s / 1000 = 1 个令牌
local new_tokens = math.floor((time_diff * rate) / 1000)

-- 更新令牌数
if new_tokens > 0 then
    current_tokens = math.min(capacity, current_tokens + new_tokens)
    last_time = now -- 只有产生了新令牌或者消耗了令牌才更新时间，防止精度丢失
end

-- 判断是否允许通过
local allowed = 0
if current_tokens >= requested then
    current_tokens = current_tokens - requested
    allowed = 1
end

-- 更新 Redis
redis.call('HMSET', key, 'tokens', current_tokens, 'last_time', last_time)

-- 设置过期时间：桶填满所需时间 * 2，至少 60 秒
local fill_time = math.ceil(capacity / rate)
local ttl = math.max(60, fill_time * 2)
redis.call('EXPIRE', key, ttl)

return allowed
`

// ==================== Redis 限流器 ====================

// RedisRateLimiter 基于 Redis 的 IP 级别限流器
type RedisRateLimiter struct {
	redisClient *redis.Client
	rate        float64 // 每秒产生的令牌数
	burst       int     // 令牌桶容量
	mu          *sync.RWMutex
	failOpen    bool // 降级标志：true 表示 Redis 不可用，降级放行
}

// NewRedisRateLimiter 创建 Redis 限流器
// rate: 每秒产生的令牌数 (如: 10.0 表示每秒10个令牌)
// burst: 令牌桶容量 (如: 20 表示桶最多20个令牌)
func NewRedisRateLimiter(rate float64, burst int) *RedisRateLimiter {
	return &RedisRateLimiter{
		rate:     rate,
		burst:    burst,
		mu:       &sync.RWMutex{},
		failOpen: false, // 初始不降级
	}
}

// RedisSetClient 设置 Redis 客户端
// 使用延迟初始化避免循环依赖
func (r *RedisRateLimiter) RedisSetClient(redisClient *redis.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redisClient = redisClient
}

// Allow 检查是否允许请求通过
// key: Redis 限流 key (如: rate:limit:ip:{ip})
// 返回值：
//   - bool: true 表示允许通过，false 表示被限流
//   - error: 错误信息，Redis 不可用时降级返回 nil
func (r *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	// 使用 RLock 读取 client，减少锁竞争
	r.mu.RLock()
	client := r.redisClient
	r.mu.RUnlock()

	if client == nil {
		// Redis 客户端未初始化，降级放行
		return true, nil
	}

	// 计算令牌桶参数
	now := time.Now().UnixMilli() // 当前时间戳（毫秒）

	// 【修正点】直接传 rate 给 Lua 脚本，由 Lua 内部除以 1000 计算毫秒精度
	// KEYS[1]: key
	// ARGV[1]: now (当前时间戳，毫秒)
	// ARGV[2]: r.burst (桶容量)
	// ARGV[3]: r.rate (每秒产生的令牌数，不要乘 1000)
	// ARGV[4]: 1 (每次请求消耗的令牌数)

	// 优化：给 Redis 操作加一个独立的短超时（50ms），防止 Redis 响应慢拖死网关
	redisCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	cmd := client.Eval(redisCtx, luaTokenBucketRedis, []string{key}, now, r.burst, r.rate, 1)
	result, err := cmd.Result()

	if err != nil {
		// 检查是否为 Redis 连接错误或超时
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// 超时或取消，记录错误并降级放行
			logger.Warn(ctx, "Redis 限流检查超时，降级放行",
				logger.String("key", key),
				logger.ErrorField("error", err),
			)
			return true, nil
		}

		// 其他 Redis 错误
		logger.Error(ctx, "Redis 限流检查失败，降级放行",
			logger.String("key", key),
			logger.ErrorField("error", err),
		)
		return true, nil
	}

	// 检查 Lua 脚本返回值
	// 返回 1 表示允许通过，0 表示被限流
	allowed, ok := result.(int64)
	if !ok {
		// 类型断言失败，降级放行
		logger.Warn(ctx, "Redis 限流返回值类型错误，降级放行",
			logger.String("key", key),
			logger.Any("result", result),
		)
		return true, nil
	}

	return allowed == 1, nil
}

// CheckBlacklist 检查 IP 是否在黑名单中
// blacklistKey: Redis 黑名单 Set 的 key (如: gateway:blacklist:ips)
// ip: 要检查的 IP 地址
// 返回值：
//   - bool: true 表示在黑名单中，false 表示不在
//   - error: 错误信息，Redis 不可用时降级返回 nil
func CheckBlacklist(ctx context.Context, blacklistKey, ip string) (bool, error) {
	// 获取 Redis 客户端
	client := pkgredis.Client()
	if client == nil {
		// Redis 客户端未初始化，降级放行（不在黑名单）
		return false, nil
	}

	// 检查 IP 是否在黑名单 Set 中
	// 使用 SISMEMBER 命令
	cmd := client.SIsMember(ctx, blacklistKey, ip)
	exists, err := cmd.Result()
	if err != nil {
		// 检查是否为 Redis 连接错误
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// 超时或取消，记录错误并降级放行
			logger.Warn(ctx, "Redis 黑名单检查超时，降级放行",
				logger.String("ip", ip),
				logger.ErrorField("error", err),
			)
			return false, nil
		}

		// 其他 Redis 错误
		logger.Error(ctx, "Redis 黑名单检查失败，降级放行",
			logger.String("ip", ip),
			logger.ErrorField("error", err),
		)
		return false, nil
	}

	return exists, nil
}

// ==================== Redis 限流中间件 ====================

// 全局 Redis 限流器实例
var globalRedisLimiter *RedisRateLimiter

// InitRedisRateLimiter 初始化全局 Redis 限流器
// rate: 每秒产生的令牌数
// burst: 令牌桶容量
// redisClient: Redis 客户端实例
func InitRedisRateLimiter(rate float64, burst int, redisClient *redis.Client) {
	globalRedisLimiter = NewRedisRateLimiter(rate, burst)

	// 设置 Redis 客户端
	globalRedisLimiter.RedisSetClient(redisClient)

	logger.Info(context.Background(), "Redis 限流器初始化完成",
		logger.Float64("rate", rate),
		logger.Int("burst", burst),
	)
}

// ==================== 原有的内存限流器 (保留向后兼容) ====================

// UserRateLimiter 用户级别的限流器
// 为每个用户维护独立的令牌桶
type UserRateLimiter struct {
	limiters map[string]*rate.Limiter // key: user_uuid, value: 令牌桶
	mu       *sync.RWMutex
	r        rate.Limit // 每秒产生的令牌数
	b        int        // 令牌桶容量
}

// NewUserRateLimiter 创建用户级别限流器
// requestsPerSecond: 每秒允许的请求数（令牌产生速率）
// burst: 令牌桶容量（允许的突发请求数）
func NewUserRateLimiter(requestsPerSecond float64, burst int) *UserRateLimiter {
	return &UserRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		mu:       &sync.RWMutex{},
		r:        rate.Limit(requestsPerSecond),
		b:        burst,
	}
}

// GetLimiter 获取指定用户的限流器
// 如果用户的限流器不存在，则创建一个新的
func (u *UserRateLimiter) GetLimiter(userUUID string) *rate.Limiter {
	u.mu.Lock()
	defer u.mu.Unlock()

	limiter, exists := u.limiters[userUUID]
	if !exists {
		// 为新用户创建令牌桶
		limiter = rate.NewLimiter(u.r, u.b)
		u.limiters[userUUID] = limiter
	}

	return limiter
}

// CleanupInactiveLimiters 清理长时间未使用的限流器
// 定期调用此方法可以释放内存
func (u *UserRateLimiter) CleanupInactiveLimiters(inactiveDuration time.Duration) {
	u.mu.Lock()
	defer u.mu.Unlock()

	for userUUID, limiter := range u.limiters {
		// 检查令牌桶是否长时间未使用
		// 如果令牌桶已满，说明很久没有请求了
		if limiter.Tokens() >= float64(u.b) {
			// 简单策略：删除令牌桶已满的用户
			// 更精确的做法需要记录最后使用时间
			delete(u.limiters, userUUID)
		}
	}
}

// GetLimiterCount 获取当前限流器数量（用于监控）
func (u *UserRateLimiter) GetLimiterCount() int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.limiters)
}

// 全局用户限流器实例
var globalUserLimiter *UserRateLimiter

// InitUserRateLimiter 初始化全局用户限流器
func InitUserRateLimiter(requestsPerSecond float64, burst int) {
	globalUserLimiter = NewUserRateLimiter(requestsPerSecond, burst)

	// 启动定期清理协程（每小时清理一次）
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			if globalUserLimiter != nil {
				globalUserLimiter.CleanupInactiveLimiters(30 * time.Minute)
			}
		}
	}()
}

// UserRateLimitMiddleware 用户级别限流中间件
// 必须在 JWT 认证中间件之后使用，因为需要从 context 中获取 user_uuid
func UserRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 context 中获取用户 UUID（由 JWT 中间件设置）
		userUUID, exists := GetUserUUID(c)
		if !exists || userUUID == "" {
			// 如果没有用户信息，说明是公开接口或者认证失败
			// 这种情况应该已经被前面的中间件拦截了
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "未认证，无法进行限流检查",
			})
			c.Abort()
			return
		}

		// 获取该用户的限流器
		limiter := globalUserLimiter.GetLimiter(userUUID)

		// 尝试获取令牌
		if !limiter.Allow() {
			// 没有可用令牌，请求被限流
			logger.Warn(c.Request.Context(), "用户请求被限流",
				logger.String("user_uuid", userUUID),
				logger.String("path", c.Request.URL.Path),
				logger.String("method", c.Request.Method),
			)

			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}

		// 通过限流检查，继续处理请求
		c.Next()
	}
}

// UserRateLimitMiddlewareWithConfig 可配置的用户限流中间件
// 允许为不同的路由组设置不同的限流参数
func UserRateLimitMiddlewareWithConfig(requestsPerSecond float64, burst int) gin.HandlerFunc {
	// 创建独立的限流器实例
	limiter := NewUserRateLimiter(requestsPerSecond, burst)

	return func(c *gin.Context) {
		userUUID, exists := GetUserUUID(c)
		if !exists || userUUID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "未认证，无法进行限流检查",
			})
			c.Abort()
			return
		}

		userLimiter := limiter.GetLimiter(userUUID)

		if !userLimiter.Allow() {
			logger.Warn(c.Request.Context(), "用户请求被限流",
				logger.String("user_uuid", userUUID),
				logger.String("path", c.Request.URL.Path),
				logger.String("method", c.Request.Method),
			)

			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ==================== Redis IP 限流中间件 ====================

// IPRateLimitMiddleware 基于 Redis 的 IP 级别限流中间件
// 支持黑名单检查、令牌桶限流、降级策略
// 参数：
//   - blacklistKey: 黑名单 Redis Set 的 key (如: gateway:blacklist:ips)
//   - rate: 每秒产生的令牌数
//   - burst: 令牌桶容量
//
// 使用示例：
//
//	router.Use(IPRateLimitMiddleware("gateway:blacklist:ips", 10, 20))
func IPRateLimitMiddleware(blacklistKey string, rate float64, burst int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. 获取客户端 IP
		ip, exists := GetClientIPSafe(c)
		if !exists || ip == "" {
			// 无法获取 IP，放行请求（记录警告）
			logger.Warn(ctx, "无法获取客户端 IP，跳过限流检查",
				logger.String("path", c.Request.URL.Path),
			)
			c.Next()
			return
		}

		// 2. 检查 IP 黑名单
		inBlacklist, err := CheckBlacklist(ctx, blacklistKey, ip)
		if err != nil {
			// Redis 错误，已经降级放行了，记录日志即可
			// 继续后续流程
		} else if inBlacklist {
			// IP 在黑名单中，直接拒绝
			logger.Warn(ctx, "IP 在黑名单中，拒绝访问",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
				logger.String("method", c.Request.Method),
			)

			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "访问被禁止，请联系管理员",
			})
			c.Abort()
			return
		}

		// 3. 执行 IP 限流检查
		if globalRedisLimiter == nil {
			// 限流器未初始化，放行请求
			logger.Warn(ctx, "Redis 限流器未初始化，跳过限流检查",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
			)
			c.Next()
			return
		}

		// 构造限流 key: rate:limit:ip:{ip}
		rateLimitKey := fmt.Sprintf("rate:limit:ip:%s", ip)

		// 检查是否允许通过
		allowed, err := globalRedisLimiter.Allow(ctx, rateLimitKey)
		if err != nil {
			// Redis 错误，已经降级放行了（返回 true）
			// 继续后续流程
			logger.Warn(ctx, "Redis 限流检查异常，降级放行",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
				logger.ErrorField("error", err),
			)
		} else if !allowed {
			// 被限流
			logger.Warn(ctx, "IP 请求被限流",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
				logger.String("method", c.Request.Method),
			)

			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}

		// 4. 通过检查，继续处理请求
		c.Next()
	}
}

// IPRateLimitMiddlewareWithConfig 可配置的 Redis IP 限流中间件
// 允许为不同的路由组设置不同的限流参数
// 参数：
//   - blacklistKey: 黑名单 Redis Set 的 key
//   - rate: 每秒产生的令牌数
//   - burst: 令牌桶容量
//
// 使用示例：
//
//	api.GET("/sensitive", IPRateLimitMiddlewareWithConfig("gateway:blacklist:ips", 5, 10), handler)
func IPRateLimitMiddlewareWithConfig(blacklistKey string, rate float64, burst int) gin.HandlerFunc {
	// 创建独立的限流器实例
	limiter := NewRedisRateLimiter(rate, burst)

	// 2. 使用 sync.Once 懒加载 Redis Client（只执行一次，避免每次请求都加锁）
	var once sync.Once

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 懒加载 Redis Client，只执行一次
		once.Do(func() {
			if client := pkgredis.Client(); client != nil {
				limiter.RedisSetClient(client)
			}
		})

		// 1. 获取客户端 IP
		ip, exists := GetClientIPSafe(c)
		if !exists || ip == "" {
			// 无法获取 IP，放行请求（记录警告）
			logger.Warn(ctx, "无法获取客户端 IP，跳过限流检查",
				logger.String("path", c.Request.URL.Path),
			)
			c.Next()
			return
		}

		// 2. 检查 IP 黑名单
		inBlacklist, err := CheckBlacklist(ctx, blacklistKey, ip)
		if err != nil {
			// Redis 错误，已经降级放行了，记录日志即可
			// 继续后续流程
		} else if inBlacklist {
			// IP 在黑名单中，直接拒绝
			logger.Warn(ctx, "IP 在黑名单中，拒绝访问",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
				logger.String("method", c.Request.Method),
			)

			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "访问被禁止，请联系管理员",
			})
			c.Abort()
			return
		}

		// 3. 执行 IP 限流检查（Redis Client 已在初始化时设置）
		// limiter.RedisSetClient(pkgredis.Client())

		// 构造限流 key: rate:limit:ip:{ip}
		rateLimitKey := fmt.Sprintf("rate:limit:ip:%s", ip)

		// 检查是否允许通过
		allowed, err := limiter.Allow(ctx, rateLimitKey)
		if err != nil {
			// Redis 错误，已经降级放行了（返回 true）
			// 继续后续流程
			logger.Warn(ctx, "Redis 限流检查异常，降级放行",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
				logger.ErrorField("error", err),
			)
		} else if !allowed {
			// 被限流
			logger.Warn(ctx, "IP 请求被限流",
				logger.String("ip", ip),
				logger.String("path", c.Request.URL.Path),
				logger.String("method", c.Request.Method),
			)

			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}

		// 4. 通过检查，继续处理请求
		c.Next()
	}
}
