package config

import "time"

// RedisConfig 单实例 Redis 配置。
// 仅需一套连接，容器场景建议仍走 stdout 观测，Redis 保持轻量连接池。
type RedisConfig struct {
	Addr         string        `json:"addr" yaml:"addr"`                 // host:port
	Password     string        `json:"password" yaml:"password"`         // 可空
	DB           int           `json:"db" yaml:"db"`                     // DB 索引，默认 0
	PoolSize     int           `json:"poolSize" yaml:"poolSize"`         // 连接池大小
	MinIdleConns int           `json:"minIdleConns" yaml:"minIdleConns"` // 最小空闲连接
	DialTimeout  time.Duration `json:"dialTimeout" yaml:"dialTimeout"`   // 建连超时
	ReadTimeout  time.Duration `json:"readTimeout" yaml:"readTimeout"`   // 读超时
	WriteTimeout time.Duration `json:"writeTimeout" yaml:"writeTimeout"` // 写超时
	PoolTimeout  time.Duration `json:"poolTimeout" yaml:"poolTimeout"`   // 从池获取连接超时
	ConnMaxIdle  time.Duration `json:"connMaxIdle" yaml:"connMaxIdle"`   // 连接最大空闲时间（对应 go-redis ConnMaxIdleTime）
	// 重试
	RetryOnConnectFailure bool `json:"retryOnConnectFailure" yaml:"retryOnConnectFailure"` // 连接失败时重试
	MaxRetries int `json:"maxRetries" yaml:"maxRetries"` // 最大重试次数
	MinRetryBackoff time.Duration `json:"minRetryBackoff" yaml:"minRetryBackoff"` // 最小重试间隔
	MaxRetryBackoff time.Duration `json:"maxRetryBackoff" yaml:"maxRetryBackoff"` // 最大重试间隔
}

// DefaultRedisConfig 返回本地开发的默认配置。
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		// 与 docker-compose.yml 对齐：host redis，默认无密码
		Addr:         "redis:6379",
		Password:     "",
		DB:           0,
		PoolSize:     20,
		MinIdleConns: 4,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolTimeout:  4 * time.Second,
		ConnMaxIdle:  5 * time.Minute,
		RetryOnConnectFailure: true,
		MaxRetries: 3,
		MinRetryBackoff: 8 * time.Millisecond,  // 最小重试间隔8ms
		MaxRetryBackoff: 512 * time.Millisecond,  // 最大重试间隔512ms
	}
}
