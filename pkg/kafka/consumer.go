package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// ==================== Consumer 定义 ====================

type Consumer struct {
	reader      *kafka.Reader
	redisClient *redis.Client
	producer    *Producer // 用于重新发送失败的任务
}

// NewConsumer 创建 Redis 重试队列消费者
func NewConsumer(brokers []string, topic, groupID string, redisClient *redis.Client, producer *Producer) *Consumer {
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:        brokers,
			Topic:          topic,
			GroupID:        groupID,
			MinBytes:       1,
			MaxBytes:       10 << 20, // 10MB
			CommitInterval: time.Second,
			StartOffset:    kafka.LastOffset,
		}),
		redisClient: redisClient,
		producer:    producer,
	}
}

// NewConsumerWithConfig 使用配置创建消费者
func NewConsumerWithConfig(brokers []string, topic string, cfg kafka.ReaderConfig, redisClient *redis.Client, producer *Producer) *Consumer {
	cfg.Brokers = brokers
	cfg.Topic = topic
	return &Consumer{
		reader:      kafka.NewReader(cfg),
		redisClient: redisClient,
		producer:    producer,
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}

// ==================== 消费和重试逻辑 ====================

// Start 启动消费者，阻塞式运行
func (c *Consumer) Start(ctx context.Context, logger Logger) error {
	logger.Info(ctx, "Redis 重试队列消费者启动", nil)

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, "Redis 重试队列消费者停止", nil)
			return ctx.Err()
		default:
			// 读取消息
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				logger.Error(ctx, "读取 Kafka 消息失败", map[string]interface{}{"error": err.Error()})
				time.Sleep(time.Second) // 避免错误风暴
				continue
			}

			// 处理消息
			if err := c.processMessage(ctx, msg, logger); err != nil {
				logger.Error(ctx, "处理 Redis 重试任务失败", map[string]interface{}{
					"error":  err.Error(),
					"offset": msg.Offset,
				})
			}

			// 提交消息（无论成功失败都提交，避免重复消费同一条消息）
			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				logger.Error(ctx, "提交 Kafka 消息失败", map[string]interface{}{"error": err.Error()})
			}
		}
	}
}

// processMessage 处理单条消息
func (c *Consumer) processMessage(ctx context.Context, msg kafka.Message, logger Logger) error {
	// 解析任务
	var task RedisTask
	if err := json.Unmarshal(msg.Value, &task); err != nil {
		return fmt.Errorf("解析 Redis 任务失败: %w", err)
	}

	logger.Info(ctx, "处理 Redis 重试任务", map[string]interface{}{
		"type":        task.Type,
		"retry_count": task.RetryCount,
		"trace_id":    task.TraceID,
	})

	// 执行 Redis 操作
	err := c.executeRedisTask(ctx, task)
	if err != nil {
		// 如果还没达到最大重试次数，重新发送到 Kafka
		if task.RetryCount < task.MaxRetries {
			task.RetryCount++
			if retryErr := c.producer.SendRedisTask(ctx, task); retryErr != nil {
				logger.Error(ctx, "重新发送 Redis 任务到 Kafka 失败", map[string]interface{}{
					"error":       retryErr.Error(),
					"retry_count": task.RetryCount,
				})
			} else {
				logger.Info(ctx, "Redis 任务重新发送到队列", map[string]interface{}{
					"retry_count": task.RetryCount,
					"max_retries": task.MaxRetries,
				})
			}
		} else {
			// 达到最大重试次数，记录错误并放弃
			logger.Error(ctx, "Redis 任务达到最大重试次数，放弃处理", map[string]interface{}{
				"error":       err.Error(),
				"retry_count": task.RetryCount,
				"max_retries": task.MaxRetries,
				"task":        task,
			})
		}
		return err
	}

	logger.Info(ctx, "Redis 重试任务执行成功", map[string]interface{}{
		"type":        task.Type,
		"retry_count": task.RetryCount,
	})
	return nil
}

// executeRedisTask 执行 Redis 任务
func (c *Consumer) executeRedisTask(ctx context.Context, task RedisTask) error {
	switch task.Type {
	case CmdSimple:
		return c.executeSimpleCommand(ctx, task)
	case CmdPipeline:
		return c.executePipeline(ctx, task)
	case CmdLua:
		return c.executeLuaScript(ctx, task)
	default:
		return fmt.Errorf("未知的命令类型: %s", task.Type)
	}
}

// executeSimpleCommand 执行简单命令
func (c *Consumer) executeSimpleCommand(ctx context.Context, task RedisTask) error {
	args := make([]interface{}, 0, len(task.Args)+1)
	args = append(args, task.Command)
	args = append(args, task.Args...)
	cmd := c.redisClient.Do(ctx, args...)
	return cmd.Err()
}

// executePipeline 执行 Pipeline
func (c *Consumer) executePipeline(ctx context.Context, task RedisTask) error {
	pipe := c.redisClient.Pipeline()

	for _, cmd := range task.PipelineCmds {
		args := make([]interface{}, 0, len(cmd.Args)+1)
		args = append(args, cmd.Command)
		args = append(args, cmd.Args...)
		pipe.Do(ctx, args...)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// executeLuaScript 执行 Lua 脚本
func (c *Consumer) executeLuaScript(ctx context.Context, task RedisTask) error {
	script := redis.NewScript(task.LuaScript)
	cmd := script.Run(ctx, c.redisClient, task.LuaKeys, task.LuaArgs...)
	return cmd.Err()
}

// ==================== Logger 接口 ====================

// Logger 定义消费者需要的日志接口（避免直接依赖 pkg/logger）
type Logger interface {
	Info(ctx context.Context, msg string, fields map[string]interface{})
	Error(ctx context.Context, msg string, fields map[string]interface{})
}
