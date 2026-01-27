package kafka

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
)

// ==================== Producer 定义 ====================

// Producer Kafka 生产者（通用）
type Producer struct {
	writer *kafka.Writer
}

// NewProducer 创建 Kafka 生产者
func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
		},
	}
}

// Send 发送消息到 Kafka
func (p *Producer) Send(ctx context.Context, data []byte) error {
	return p.writer.WriteMessages(ctx, kafka.Message{
		Value: data,
		Time:  time.Now(),
	})
}

// Close 关闭生产者
func (p *Producer) Close() error {
	return p.writer.Close()
}
