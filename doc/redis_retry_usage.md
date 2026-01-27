# Redis 重试机制使用指南

## 概述

本项目实现了基于 Kafka 的 Redis 操作重试机制。当 Redis 的增删改操作失败时，会自动将任务发送到 Kafka 队列进行异步重试，提高系统的可靠性。

## 架构设计

```
Repository 层
    ↓ (Redis 操作失败)
LogAndRetryRedisError()
    ↓
Kafka Producer (发送任务到队列)
    ↓
redis-retry-queue (Kafka Topic)
    ↓
Kafka Consumer (后台消费)
    ↓
重新执行 Redis 操作
```

## 配置

### 1. Kafka 配置 (config/kafka.go)

默认配置：
- Brokers: `kafka:9092`
- Topic: `redis-retry-queue`
- Consumer Group: `redis-retry-consumer-group`
- 最大重试次数: 3次

### 2. 启动服务

Kafka Producer 和 Consumer 在 `apps/user/cmd/main.go` 中自动初始化，无需手动配置。

## 使用方法

### 场景 1: 简单的 DEL 操作

```go
func (r *UserRepository) DeleteUserCache(ctx context.Context, userUUID string) error {
    key := fmt.Sprintf("user:info:%s", userUUID)
    
    // 执行 Redis 删除操作
    err := r.redis.Del(ctx, key).Err()
    if err != nil {
        // 构造重试任务
        task := kafka.BuildDelTask(key).
            WithSource("UserRepository.DeleteUserCache")
        
        // 发送到重试队列
        LogAndRetryRedisError(ctx, task, err)
        
        return WrapRedisError(err)
    }
    
    return nil
}
```

### 场景 2: SET 操作（带 TTL）

```go
func (r *DeviceRepository) SaveToken(ctx context.Context, deviceID string, token string, ttl time.Duration) error {
    key := fmt.Sprintf("device:token:%s", deviceID)
    
    // 执行 Redis SET 操作
    err := r.redis.Set(ctx, key, token, ttl).Err()
    if err != nil {
        // 构造重试任务
        task := kafka.BuildSetTask(key, token, ttl).
            WithSource("DeviceRepository.SaveToken")
        
        // 发送到重试队列
        LogAndRetryRedisError(ctx, task, err)
        
        return WrapRedisError(err)
    }
    
    return nil
}
```

### 场景 3: HSET 操作

```go
func (r *UserRepository) UpdateUserField(ctx context.Context, userUUID string, field string, value interface{}) error {
    key := fmt.Sprintf("user:profile:%s", userUUID)
    
    // 执行 Redis HSET 操作
    err := r.redis.HSet(ctx, key, field, value).Err()
    if err != nil {
        // 构造重试任务
        task := kafka.BuildHSetTask(key, field, value).
            WithSource("UserRepository.UpdateUserField")
        
        // 发送到重试队列
        LogAndRetryRedisError(ctx, task, err)
        
        return WrapRedisError(err)
    }
    
    return nil
}
```

### 场景 4: Pipeline 批量操作

```go
func (r *UserRepository) DeleteUserCacheAndRelation(ctx context.Context, userUUID string) error {
    // 准备 Pipeline 命令
    cmds := []kafka.RedisCmd{
        {Command: "del", Args: []interface{}{fmt.Sprintf("user:info:%s", userUUID)}},
        {Command: "del", Args: []interface{}{fmt.Sprintf("user:relation:%s", userUUID)}},
        {Command: "del", Args: []interface{}{fmt.Sprintf("user:session:%s", userUUID)}},
    }
    
    // 执行 Pipeline
    pipe := r.redis.Pipeline()
    for _, cmd := range cmds {
        args := make([]interface{}, 0, len(cmd.Args)+1)
        args = append(args, cmd.Command)
        args = append(args, cmd.Args...)
        pipe.Do(ctx, args...)
    }
    
    _, err := pipe.Exec(ctx)
    if err != nil {
        // 构造重试任务
        task := kafka.BuildPipelineTask(cmds).
            WithSource("UserRepository.DeleteUserCacheAndRelation")
        
        // 发送到重试队列
        LogAndRetryRedisError(ctx, task, err)
        
        return WrapRedisError(err)
    }
    
    return nil
}
```

### 场景 5: Lua 脚本

```go
func (r *FriendRepository) AtomicAddFriend(ctx context.Context, userUUID, friendUUID string) error {
    // Lua 脚本（原子性添加好友关系）
    script := `
        redis.call('sadd', KEYS[1], ARGV[1])
        redis.call('sadd', KEYS[2], ARGV[2])
        return 1
    `
    
    keys := []string{
        fmt.Sprintf("user:friends:%s", userUUID),
        fmt.Sprintf("user:friends:%s", friendUUID),
    }
    args := []interface{}{friendUUID, userUUID}
    
    // 执行 Lua 脚本
    err := r.redis.Eval(ctx, script, keys, args...).Err()
    if err != nil {
        // 构造重试任务
        task := kafka.BuildLuaTask(script, keys, args...).
            WithSource("FriendRepository.AtomicAddFriend")
        
        // 发送到重试队列
        LogAndRetryRedisError(ctx, task, err)
        
        return WrapRedisError(err)
    }
    
    return nil
}
```

## 构造器函数 (Builder Functions)

### 可用的构造器

| 函数 | 用途 | 示例 |
|------|------|------|
| `BuildDelTask(key)` | 删除键 | `BuildDelTask("user:1")` |
| `BuildSetTask(key, val, ttl)` | 设置键值（带TTL） | `BuildSetTask("token:123", "abc", 5*time.Minute)` |
| `BuildHSetTask(key, field, value)` | Hash 字段设置 | `BuildHSetTask("user:1", "name", "Alice")` |
| `BuildHDelTask(key, fields...)` | Hash 字段删除 | `BuildHDelTask("user:1", "cache", "temp")` |
| `BuildSAddTask(key, members...)` | Set 添加成员 | `BuildSAddTask("friends:1", "user2", "user3")` |
| `BuildSRemTask(key, members...)` | Set 删除成员 | `BuildSRemTask("friends:1", "user2")` |
| `BuildPipelineTask(cmds)` | Pipeline 批量操作 | 见场景4 |
| `BuildLuaTask(script, keys, args)` | Lua 脚本执行 | 见场景5 |

### 链式方法

所有构造器返回的 `RedisTask` 都支持以下链式方法：

```go
task := kafka.BuildDelTask("user:1").
    WithContext(ctx).              // 添加上下文信息（trace_id, user_uuid, device_id）
    WithError(err).                // 添加原始错误信息
    WithSource("UserRepo.Delete"). // 添加来源标识
    WithMaxRetries(5)              // 设置最大重试次数（默认3次）
```

## 监控和告警

### 日志示例

**正常重试：**
```json
{
  "level": "error",
  "msg": "Redis 操作失败，发送到重试队列",
  "error": "connection refused",
  "task_type": "simple",
  "command": "del",
  "trace_id": "abc123",
  "user_uuid": "user123"
}
```

**Kafka 发送失败：**
```json
{
  "level": "error",
  "msg": "发送 Redis 重试任务到 Kafka 失败，放弃处理",
  "kafka_error": "kafka: connection timeout",
  "original_error": "redis: connection refused",
  "task_type": "simple"
}
```

**达到最大重试次数：**
```json
{
  "level": "error",
  "msg": "Redis 任务达到最大重试次数，放弃处理",
  "error": "redis: connection refused",
  "retry_count": 3,
  "max_retries": 3
}
```

### 告警规则建议

1. **Kafka 发送失败率 > 5%**：检查 Kafka 集群状态
2. **重试任务达到最大次数 > 10次/小时**：检查 Redis 集群状态
3. **重试队列积压 > 1000条**：考虑扩容消费者

## 注意事项

### 1. 只重试增删改操作

查询操作（GET, HGET, EXISTS 等）失败不需要重试，直接返回错误即可：

```go
// ❌ 不要对查询操作使用重试
val, err := r.redis.Get(ctx, key).Result()
if err != nil {
    return "", WrapRedisError(err)  // 直接返回错误
}
```

### 2. 幂等性要求

所有发送到重试队列的操作必须是幂等的（多次执行结果相同）：

- ✅ DEL, SET, HSET, HDEL（幂等）
- ✅ SADD, SREM（幂等）
- ⚠️ INCR, DECR（非幂等，需要特殊处理）
- ⚠️ LPUSH, RPUSH（非幂等，需要特殊处理）

### 3. 性能考虑

- Kafka 发送是异步的，不会阻塞主流程
- 如果 Kafka Producer 未初始化，会静默失败（不影响业务）
- 消费者在后台单独的 goroutine 中运行

### 4. 数据一致性

Redis 重试机制不保证强一致性，只保证最终一致性：
- 如果重试失败，数据可能永久丢失
- 建议配合 MySQL 持久化层使用
- 关键操作应该先写 MySQL，再更新 Redis

## 示例：完整的 Repository 方法

```go
func (r *UserRepository) DeleteUser(ctx context.Context, userUUID string) error {
    // 1. 先删除 MySQL（持久化层）
    if err := r.db.Where("uuid = ?", userUUID).Delete(&model.UserInfo{}).Error; err != nil {
        return WrapDBError(err)
    }
    
    // 2. 删除 Redis 缓存（可失败）
    key := fmt.Sprintf("user:info:%s", userUUID)
    if err := r.redis.Del(ctx, key).Err(); err != nil {
        // 发送到重试队列，但不影响主流程
        task := kafka.BuildDelTask(key).
            WithSource("UserRepository.DeleteUser")
        LogAndRetryRedisError(ctx, task, err)
        
        // 记录警告日志但继续执行
        logger.Warn(ctx, "删除用户缓存失败，已发送到重试队列",
            logger.String("user_uuid", userUUID),
            logger.ErrorField("error", err),
        )
    }
    
    return nil
}
```

## 故障排查

### Kafka Consumer 没有启动

检查日志中是否有：
```
"msg": "Redis 重试队列消费者启动"
```

### 任务没有被消费

1. 检查 Kafka Topic 是否存在：`kafka-topics --list`
2. 检查消费者组状态：`kafka-consumer-groups --group redis-retry-consumer-group --describe`
3. 检查 Redis 连接状态

### 重试一直失败

1. 检查 Redis 集群是否正常
2. 检查网络连接
3. 查看错误日志，确认错误原因
4. 考虑增加重试次数或调整重试策略

## 未来优化

1. **支持延迟重试**：指数退避策略
2. **死信队列**：达到最大重试次数后转移到死信队列
3. **重试优先级**：关键操作优先重试
4. **动态配置**：支持运行时调整重试参数
5. **监控面板**：可视化重试队列状态
