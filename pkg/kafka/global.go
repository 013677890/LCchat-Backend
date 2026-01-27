package kafka

import (
	"context"
	"sync"
)

// ==================== 全局 Producer 实例管理 ====================

var (
	globalProducer *Producer
	producerMu     sync.RWMutex
)

// SetGlobalProducer 设置全局 Kafka Producer 实例
// 应在应用启动时调用一次
func SetGlobalProducer(producer *Producer) {
	producerMu.Lock()
	defer producerMu.Unlock()
	globalProducer = producer
}

// GetGlobalProducer 获取全局 Kafka Producer 实例
func GetGlobalProducer() *Producer {
	producerMu.RLock()
	defer producerMu.RUnlock()
	return globalProducer
}

// SendRedisTaskGlobal 使用全局 Producer 发送 Redis 任务
// 如果全局 Producer 未初始化，返回 nil（不报错，避免影响主流程）
func SendRedisTaskGlobal(ctx context.Context, task RedisTask) error {
	producer := GetGlobalProducer()
	if producer == nil {
		// Producer 未初始化，静默失败
		return nil
	}
	return producer.SendRedisTask(ctx, task)
}
