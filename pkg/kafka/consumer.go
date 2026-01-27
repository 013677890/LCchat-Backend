package kafka

import (
	"context"

	"github.com/segmentio/kafka-go"
)

// ==================== Consumer 定义 ====================

// Consumer Kafka 消费者（通用）
type Consumer struct {
	reader *kafka.Reader
}

// NewConsumer 创建 Kafka 消费者
func NewConsumer(brokers []string, topic, groupID string) *Consumer {
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			Topic:   topic,
			GroupID: groupID,
		}),
	}
}

// MessageHandler 消息处理函数类型
type MessageHandler func(ctx context.Context, message []byte) error

// Start 启动消费者（阻塞式运行）
func (c *Consumer) Start(ctx context.Context, handler MessageHandler) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 读取消息
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				continue
			}

			// 处理消息
			_ = handler(ctx, msg.Value)

			// 提交消息（无论成功失败都提交，避免重复消费）
			_ = c.reader.CommitMessages(ctx, msg)
		}
	}
}

// Close 关闭消费者
func (c *Consumer) Close() error {
	return c.reader.Close()
}
