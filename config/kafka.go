package config

import "time"

// KafkaConfig Kafka 配置
type KafkaConfig struct {
	Brokers []string `json:"brokers" yaml:"brokers"` // Kafka broker 地址列表

	// Producer 配置
	ProducerConfig KafkaProducerConfig `json:"producer" yaml:"producer"`

	// Consumer 配置
	ConsumerConfig KafkaConsumerConfig `json:"consumer" yaml:"consumer"`

	// Redis 重试队列配置
	RedisRetryTopic string `json:"redisRetryTopic" yaml:"redisRetryTopic"` // Redis 重试队列 topic
}

// KafkaProducerConfig Kafka 生产者配置
type KafkaProducerConfig struct {
	BatchSize    int           `json:"batchSize" yaml:"batchSize"`       // 批量发送大小
	BatchTimeout time.Duration `json:"batchTimeout" yaml:"batchTimeout"` // 批量发送超时
	MaxAttempts  int           `json:"maxAttempts" yaml:"maxAttempts"`   // 最大重试次数
	WriteTimeout time.Duration `json:"writeTimeout" yaml:"writeTimeout"` // 写入超时
}

// KafkaConsumerConfig Kafka 消费者配置
type KafkaConsumerConfig struct {
	GroupID           string        `json:"groupId" yaml:"groupId"`                     // 消费者组 ID
	MinBytes          int           `json:"minBytes" yaml:"minBytes"`                   // 最小读取字节数
	MaxBytes          int           `json:"maxBytes" yaml:"maxBytes"`                   // 最大读取字节数
	MaxWait           time.Duration `json:"maxWait" yaml:"maxWait"`                     // 最大等待时间
	CommitInterval    time.Duration `json:"commitInterval" yaml:"commitInterval"`       // 提交间隔
	StartOffset       int64         `json:"startOffset" yaml:"startOffset"`             // 起始偏移量 (-1:最新, -2:最早)
	HeartbeatInterval time.Duration `json:"heartbeatInterval" yaml:"heartbeatInterval"` // 心跳间隔
	SessionTimeout    time.Duration `json:"sessionTimeout" yaml:"sessionTimeout"`       // 会话超时
	RebalanceTimeout  time.Duration `json:"rebalanceTimeout" yaml:"rebalanceTimeout"`   // 重平衡超时
}

// DefaultKafkaConfig 返回本地开发的默认配置
func DefaultKafkaConfig() KafkaConfig {
	return KafkaConfig{
		// Docker compose 中的 Kafka 地址
		Brokers:         []string{"kafka:9092"},
		RedisRetryTopic: "redis-retry-queue",

		ProducerConfig: KafkaProducerConfig{
			BatchSize:    100,
			BatchTimeout: 10 * time.Millisecond,
			MaxAttempts:  3,
			WriteTimeout: 10 * time.Second,
		},

		ConsumerConfig: KafkaConsumerConfig{
			GroupID:           "redis-retry-consumer-group",
			MinBytes:          1,        // 1 字节即可触发读取
			MaxBytes:          10 << 20, // 10MB
			MaxWait:           500 * time.Millisecond,
			CommitInterval:    1 * time.Second,
			StartOffset:       -1, // 从最新消息开始消费
			HeartbeatInterval: 3 * time.Second,
			SessionTimeout:    10 * time.Second,
			RebalanceTimeout:  60 * time.Second,
		},
	}
}
