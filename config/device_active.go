package config

import "time"

// DeviceActiveConfig 设备活跃时间同步配置（Gateway / Connect 通用）。
type DeviceActiveConfig struct {
	// ShardCount 节流分片数量（分片锁 map）。
	ShardCount int `json:"shardCount" yaml:"shardCount"`
	// UpdateInterval 超过该间隔才会再次触发活跃时间同步。
	UpdateInterval time.Duration `json:"updateInterval" yaml:"updateInterval"`
	// FlushInterval 缓冲 map 批量消费周期。
	FlushInterval time.Duration `json:"flushInterval" yaml:"flushInterval"`
	// WorkerCount 异步同步工作协程数。
	WorkerCount int `json:"workerCount" yaml:"workerCount"`
	// QueueSize 异步同步任务队列容量。
	QueueSize int `json:"queueSize" yaml:"queueSize"`
	// RPCTimeout 单次 gRPC 更新超时。
	RPCTimeout time.Duration `json:"rpcTimeout" yaml:"rpcTimeout"`
}

// DefaultDeviceActiveConfig 返回默认配置（可通过环境变量覆盖）。
// - DEVICE_ACTIVE_SHARD_COUNT: 分片数量（默认 64）
// - DEVICE_ACTIVE_UPDATE_INTERVAL_SECONDS: 同步间隔秒数（默认 480，即 8 分钟）
// - DEVICE_ACTIVE_FLUSH_INTERVAL_SECONDS: 批量消费周期秒数（默认 240，即 4 分钟）
// - DEVICE_ACTIVE_WORKER_COUNT: worker 数（默认 8）
// - DEVICE_ACTIVE_QUEUE_SIZE: 队列容量（默认 8192）
// - DEVICE_ACTIVE_RPC_TIMEOUT_MS: RPC 超时毫秒（默认 3000）
func DefaultDeviceActiveConfig() DeviceActiveConfig {
	return DeviceActiveConfig{
		ShardCount:     getenvInt("DEVICE_ACTIVE_SHARD_COUNT", 64),
		UpdateInterval: time.Duration(getenvInt("DEVICE_ACTIVE_UPDATE_INTERVAL_SECONDS", 8*60)) * time.Second,
		FlushInterval:  time.Duration(getenvInt("DEVICE_ACTIVE_FLUSH_INTERVAL_SECONDS", 4*60)) * time.Second,
		WorkerCount:    getenvInt("DEVICE_ACTIVE_WORKER_COUNT", 8),
		QueueSize:      getenvInt("DEVICE_ACTIVE_QUEUE_SIZE", 8192),
		RPCTimeout:     time.Duration(getenvInt("DEVICE_ACTIVE_RPC_TIMEOUT_MS", 3000)) * time.Millisecond,
	}
}
