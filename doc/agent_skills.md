## Agent Skills Guide

This document captures the development conventions and skills needed to work
in this project. It is intended for AI agents and new contributors.

### 1. Tech Stack Overview
- Go 1.25, layered architecture (gateway + user + msg + connect services)
- gRPC for service-to-service calls
- Gin for HTTP gateway
- GORM for database access
- Redis for cache/token/verification code/rate limit/routing table
- Kafka for async message delivery (msg.push topic)
- Protobuf + validate rules for request schema

### 2. Code Structure & Modules
- `apps/gateway/`: HTTP gateway, DTOs, routing, gRPC client calls
- `apps/user/`: user service, business logic, repositories, protobufs
  - **Architecture Note**: Currently contains two logical domains:
    1. **Core User Domain**: Authentication, Device Sessions, User Profile
    2. **Social Domain**: Friends, Blacklist
  - **Future Decoupling**: Keep these domains loosely coupled for eventual split
  - **Boundary Rule**: Social domain should not query Core domain tables directly
    (use UUIDs only; aggregate user info via gateway + user-service)
- `apps/connect/`: WebSocket connection service + gRPC push endpoint
  - `internal/svc/connect_service.go`: core types (Session, Envelope), worker pool startup
  - `internal/svc/lifecycle.go`: OnConnect/OnHeartbeat/OnDisconnect, status worker pool
  - `internal/svc/auth.go`: WebSocket token auth (JWT + Redis兜底)
  - `internal/handler/`: Gin WebSocket handler
  - `internal/manager/`: connection registry (online index)
  - `internal/grpc/`: gRPC server (PushToDevice, PushToUser, BroadcastToUsers, KickConnection)
  - **架构定位**：纯管道，不对接 Kafka，不做业务判断。上游 Push-Job 查 Redis 路由表后通过 gRPC 精确调用。
- `apps/msg/`: message service (proto 已定义，业务代码待实现)
  - Proto: `SendMessage`, `PullMessages`, `RecallMessage`, `GetConversations`, `MarkRead` 等
  - Kafka: msg-service 落库后写 `MsgPushEvent` 到 `msg.push` topic，立即返回
  - Push-Job: 消费 Kafka → 查 Redis 路由表 → gRPC 调用 Connect
- `pkg/`: shared utilities (redis, logger, util, minio)
- `consts/`: error codes and message map
- `doc/`: product and API documentation

#### 2.1 Where to Read First
- `doc/API接口规范.md`: common API conventions
- `doc/user_doc/*`: per-module API docs (auth, user, friends, blacklist)
- `doc/07-错误码.md`: full error code definitions

### 3. Conventions & Patterns

#### 3.1 Error Handling
- Business errors are encoded as `grpc/status` with numeric error codes in
  the message, then extracted in gateway via `utils.ExtractErrorCode`.
- Use `consts.IsNonServerError(code)` to decide if it is a user-facing error.
- Log internal errors with context and return `CodeInternalError`.

#### 3.2 DTO ↔ Protobuf Conversion
- Gateway request DTOs live in `apps/gateway/internal/dto`.
- Always convert DTOs to Protobuf using `ConvertToProto...` functions.
- Always convert Protobuf responses to DTO using `Convert...FromProto`.

#### 3.3 Context & Device Info
- Gateway attaches device ID via `X-Device-ID` or context key `device_id`.
- User service reads device info from context and `req.DeviceInfo`.
- Defensive coding: guard `req.DeviceInfo == nil` to avoid nil pointer.
- gRPC metadata 透传：gateway 侧写入 `trace_id/user_uuid/device_id/client_ip`，user-service 侧通过 interceptor 读取并注入 context。

#### 3.4 Logging
- Use `logger.Info/Warn/Error` and include key fields (email, device, ip).
- **禁止记录的敏感字段**:
  - ❌ `password` / `new_password` / `old_password`
  - ❌ `verify_code` / `verifyCode`
  - ❌ 完整的 `access_token` / `refresh_token`
- **允许脱敏后记录**:
  - ✅ `email` → `utils.MaskEmail(email)` (显示前3位和域名)
  - ✅ `telephone` → `utils.MaskTelephone(phone)` (显示前3后4)

#### 3.5 Redis Usage
- Verification codes stored in Redis with TTL (e.g., 2 minutes).
- Tokens stored in Redis with Access/Refresh expiry.
- Rate limiting uses counters + TTL; avoid extending TTL on every increment.
- When verification succeeds, delete the code to prevent reuse.
- **统一规范**：所有 Redis key 和 TTL 统一在 `consts/redisKey` 中定义，Repo 层只调用构造函数并使用常量 TTL。

#### 3.6 Routing Style
- Public routes grouped under `/api/v1/public/user/...`.
- Authenticated routes grouped under `/api/v1/auth/...`.
- SearchUser belongs to user domain: `/api/v1/auth/user/search`.
- Add new endpoints in:
  - `apps/gateway/internal/router/v1/auth_handle.go`
  - `apps/gateway/internal/router/router.go`
  - `apps/gateway/internal/service/*`
  - `apps/user/internal/service/*`

#### 3.7 Response Envelope
- Gateway responses always use `pkg/result/response.go`:
  - JSON: `code`, `message`, `data`, `trace_id`
  - HTTP 200 for business errors; HTTP 500 for server errors (3xxxx).

#### 3.8 Error Code Ranges
- 1xxxx: client errors (param/body/too many requests)
- 2xxxx: auth errors (token/permission)
- 11xxx: user module errors (email/password/verify code)
- 12xxx+: other modules (friend, message, group, device, blacklist)
- 3xxxx: server errors (internal, timeout, unavailable)

#### 3.9 Config & Runtime
- Configs live in `config/`:
  - `mysql.go`, `redis.go`, `logger.go`
- Local dev often uses `docker-compose.yml`.
- Database init SQL: `config/mysql/init.sql`.
- Avoid committing changes to `data/` (runtime databases).

#### 3.10 Database & Models
- Models in `model/` map to DB tables.
- Repository layer in `apps/user/internal/repository` wraps DB/Redis access.
- Use `WrapDBError` and `WrapRedisError` for consistent error mapping.
- `model/UserRelation` 关系状态：`status` 0正常 1拉黑(原先为好友) 2删除 3拉黑(原先非好友)，`blacklisted_at` 记录拉黑时间（用于黑名单排序/分页）。

#### 3.11 Protobuf & Validation
- Protos in `apps/user/pb/*.proto`.
- Validation rules via `validate/validate.proto` and generated `*.pb.validate.go`.
- After proto changes, regenerate stubs using existing tooling or scripts.

#### 3.12 Redis Key Design (Common Patterns)
每条说明格式：`key` / 数据类型 / TTL / 读写来源 / 说明

> 统一定义位置：`consts/redisKey/keys.go`（Repo 层不得再手写 key 字符串）

- `user:verify_code:{email}:{type}` / String / 业务传入 / `auth_repository` / 邮箱验证码 (type: 1注册 2登录 3重置密码 4换绑邮箱)
- `user:verify_code:1m:{email}` / Counter / 60s / `auth_repository` / 验证码分钟级限流计数
- `user:verify_code:24h:{email}` / Counter / 24h / `auth_repository` / 验证码日级限流计数
- `user:verify_code:1h:{ip}` / Counter / 1h / `auth_repository` / 验证码 IP 限流计数

- `auth:at:{user_uuid}:{device_id}` / String(MD5) / AccessToken 过期时间 / `device_repository` / AccessToken 存储
- `auth:rt:{user_uuid}:{device_id}` / String / RefreshToken 过期时间 / `device_repository` / RefreshToken 存储

- `user:devices:{user_uuid}` / Hash / 60d / `device_repository` / 设备详情缓存 (field=device_id,value=json)
- `user:devices:active:{user_uuid}` / ZSet / 7d / `device_repository` / 设备活跃时间 (member=device_id,score=unix秒)

- `user:info:{uuid}` / String(JSON) / 1h±随机抖动; 空值5m / `user_repository` / 用户信息缓存 (空值为 `{}`)

- `user:qrcode:token:{token}` / String / 48h / `user_repository` / token -> userUUID
- `user:qrcode:user:{user_uuid}` / String / 48h / `user_repository` / userUUID -> token

- `user:relation:friend:{user_uuid}` / Hash / 24h±随机抖动; 空值5m / `friend_repository` / 好友元数据(field=peer_uuid,value=json; 空值占位 `__EMPTY__`)
- `user:relation:blacklist:{user_uuid}` / ZSet / 24h±随机抖动; 空值5m / `blacklist_repository` / 拉黑集合(member=target_uuid, score=拉黑时间ms, 空值占位 `__EMPTY__`)

- `user:apply:pending:{target_uuid}` / ZSet / 24h±随机抖动; 空值5m / `apply_repository` / 待处理好友申请 (member=applicant UUID, score=created_at unix, 空值占位 `__EMPTY__`)

- `user:routing:{user_uuid}` / Hash / 无TTL(由Connect主动维护) / `connect lifecycle` / 在线路由表 (field=device_id, value=Connect节点gRPC地址如"10.0.0.5:9091")
  - Connect 建连时 HSET，心跳时续期，断连时 HDEL
  - Push-Job 查询: `HGETALL user:routing:{uuid}` 获取目标所在 Connect 节点
  - User-Service 查询: `HGET user:routing:{uuid} {device_id}` 用于精确踢线
  - 在线判断: `HLEN user:routing:{uuid}` > 0 即在线

#### 3.13 Pagination & Versioning
- 全量初始化接口的 `version` 用 **当前服务器时间**，不要用 `MAX(updated_at)`（避免删除/历史数据导致版本回退）。
- 只在 **第一页** 计算 `total` 和 `version`，后续页不重复统计，降低 DB 压力。
- 列表排序必须稳定：推荐 `created_at DESC, id DESC`。
- Offset 分页存在并发抖动，客户端需按 `uuid` 去重（服务端保证稳定排序即可）。

#### 3.14 Rate Limiting
- **IP Level Rate Limiting**: Redis-based token bucket algorithm for IP addresses.
  - Global IP limiting via `IPRateLimitMiddleware`
  - Configurable per-route via `IPRateLimitMiddlewareWithConfig`
  - Supports IP blacklist checking
- **User Level Rate Limiting**: Redis-based token bucket algorithm for authenticated users.
  - Global user limiting via `UserRateLimitMiddleware`
  - Configurable per-route via `UserRateLimitMiddlewareWithConfig`
  - Must be used after `JWTAuthMiddleware` (requires `user_uuid` in context)
- **Key Design**:
  - IP limiting: `rate:limit:ip:{ip}`
  - User limiting: `gateway:rate:limit:user:{user_uuid}`
- **Fail-Open Strategy**: When Redis is unavailable, requests are allowed to pass through.

#### 3.15 File Upload & Object Storage (MinIO)
- **MinIO Integration**: S3-compatible object storage for images, avatars, and files.
  - Config in `config/minio.go` with connection, bucket, and upload settings
  - Client wrapper in `pkg/minio/minio.go` with upload/download/delete operations
- **Security Features**:
  - **File Type Validation**: Dual validation using both file extension AND Magic Bytes (http.DetectContentType)
  - **Size Limits**: Configurable max file size (default 10MB)
  - **Allowed Types**: Whitelist of acceptable MIME types
  - **Extension Verification**: Prevents malicious files disguised with image extensions
- **Upload Flow**:
  1. Read first 512 bytes for content type detection
  2. Verify detected type matches extension (e.g., reject .exe renamed to .jpg)
  3. Check against allowed types list
  4. Upload with proper Content-Type header
- **Storage Organization**:
  - Avatars: `avatars/{uuid}.{ext}`
  - Images: `images/{date}/{uuid}.{ext}`
  - User files: `users/{user_id}/{type}/{uuid}.{ext}`
- **Access Control**: Support for public read or presigned URLs for private files
- See `doc/minio_usage.md` for detailed examples

#### 3.16 Observability
- `trace_id` generated by middleware, returned in response.
- `business_code` is stored in context for metrics middleware.

#### 3.17 Device Session & Active Time Architecture
- `lastSeenAt` 来自 `user:devices:active:{user_uuid}`，缺失时返回 0，不再补写当前时间。
- Gateway/Connect 在请求入口按分片 map 节流（默认 3 分钟）并按周期批量上报（默认 1 分钟）。
- 设备信息缓存 TTL 在登录与刷新 token 时续期。
- **写入链路**:
  1. Gateway `JWTAuthMiddleware` / Connect `OnConnect`/`OnHeartbeat` → `Syncer.Touch()`
  2. Syncer 本地分片节流（3 分钟窗口）→ pending map 缓冲
  3. 后台 `flushLoop`（1 分钟周期）swap pending → batchCh
  4. Worker 消费 → `UpdateDeviceActive` gRPC（1000 条/批）
  5. User service → `BatchSetActiveTimestamps` → Redis `ZADD + ZREMRANGEBYSCORE + EXPIRE`
- **读取链路**:
  - `GetDeviceList`/`GetOnlineStatus`/`BatchGetOnlineStatus` → Redis `ZSCORE` pipeline → cutoff 过滤
  - 在线判定：`session.Status == Online && (now - activeTime) <= OnlineWindow`
- **核心包**：`pkg/deviceactive`（Syncer，分片节流 + 缓冲 map + 后台批量消费）
- **配置**：`config/device_active.go`，归一化保证 `updateInterval < onlineWindow`、`flushInterval ≤ updateInterval`
- **降级策略**：Syncer 初始化失败不阻塞服务启动；Redis 不可用时活跃时间降级为空

#### 3.18 Async 协程池
- 协程池实现：`pkg/async`，基于 ants（Worker Pool）。
- 配置：`config/async.go`，默认 `DefaultAsyncConfig()`。
- 初始化：每个独立进程在 main 中调用 `async.Init`，并 `defer async.Release()`。
- 上下文透传：业务层通过 `async.SetContextPropagator` 注入需要透传的字段，避免在 async 包内硬编码。
- **Submit vs RunSafe 区别**:
  - `async.Submit`: 简单任务投递，无 Context 传播，无 panic 恢复
  - `async.RunSafe`: 带 Context 传播、独立超时控制、panic recover（**推荐用于 gRPC 调用**）
- **何时必须使用 RunSafe**:
  - 并发调用 gRPC 服务
  - 需要 trace_id/user_uuid 等上下文信息的异步任务
  - 父请求可能提前取消的场景（避免 context cancelled 错误）

#### 3.19 Cross-domain Aggregation (Gateway)
- 社交域只返回关系数据（UUID、备注、标签等），避免跨库依赖。
- 网关负责聚合用户信息：
  - 批量调用 user-service `BatchGetProfile` 补全昵称/头像/性别/签名
  - 搜索用户后通过 friend-service `BatchCheckIsFriend` 批量补充 isFriend
- 优先使用批量接口，避免 N+1 gRPC 调用。

#### 3.20 Connect Service Patterns
- **架构定位（方案B）**：Connect 是纯管道，不对接 Kafka，不做任何业务判断。上游 Push-Job 查 Redis 路由表后通过 gRPC 精确调用。
- **gRPC 接口**：`PushToDevice` / `PushToUser` / `BroadcastToUsers` / `KickConnection`（实现在 `internal/grpc/server.go`）。
- **Redis 路由表**：Connect 在建连/心跳时维护 `user:routing:{uuid}` Hash（field=device_id, value=grpc_addr），断连时 HDEL。
  - Push-Job 通过 HGETALL 查询目标用户所在 Connect 节点、User-Service 通过 HGET 踢线。
  - 在线判断：`HLEN user:routing:{uuid}` > 0。
- **WebSocket 生命周期**：`OnConnect` → `OnHeartbeat`(循环) → `OnDisconnect`，由 `lifecycle.go` 实现。
- **设备状态同步**：channel-based worker pool（64 worker, 8192 queue），异步调用 `UpdateDeviceStatus` RPC。
  - 队列满时丢弃任务（log Warn），不阻塞 WebSocket 处理。
  - 幂等语义：设备不存在时视为成功。
- **活跃时间同步**：复用 `pkg/deviceactive.Syncer`，BatchHandler → `UpdateDeviceActive` RPC。
  - `OnConnect` 时 `Delete()` + `Touch()` 强制绕过节流。
  - `OnDisconnect` 时 `Delete()` 清理本地缓存。
- **降级策略**：user-service gRPC 不可用时跳过设备状态和活跃时间同步，不影响连接建立。
- **优雅关闭顺序**：gRPC server → connection manager → status workers → activeSyncer → user gRPC conn → HTTP server。
- **代码拆分**：`connect_service.go`（核心类型 + 初始化）、`auth.go`（鉴权）、`lifecycle.go`（生命周期 + worker pool）。

#### 3.21 Msg Service Architecture (msg-service + Push-Job)
- **核心链路**：Gateway HTTP → msg-service gRPC (SendMessage) → Kafka `msg.push` → Push-Job → Redis 路由表 → Connect gRPC → WebSocket。
- **异步解耦**：msg-service 完成鉴权、分配 Seq、落库后，立即写 Kafka 并返回 200 OK。Kafka 是“防弹衣”。
- **Push-Job**：独立 Kafka 消费者，职责：
  1. 解码 MsgPushEvent
  2. 按 conv_type 判断扩散策略（单聊写扩散 / 群聊读扩散）
  3. 查 Redis 路由表确定目标 Connect 节点
  4. gRPC 调用 Connect 投递
  5. Self-Sync：向发送方其他在线设备同步
- **Kafka 分区键**：`conv_id`（保证同一会话的消息有序）。
- **Proto 文件**：`proto/msg/msg_service.proto`、`proto/msg/msg_common.proto`、`proto/msg/msg_push_event.proto`。
- **Model**：`model/Message.go`、`model/Conversation.go`。
- **文档**：`doc/message_doc/01~04`。

### 4. Required Skills for Future Agents

#### Go Fundamentals
- Interfaces and dependency injection
- Context propagation (`context.Context`)
- Error wrapping and comparison (`errors.Is`)

#### gRPC
- Protobuf schema updates in `apps/user/pb/*.proto`
- Regeneration of stubs/validation if needed (check existing tooling)
- Mapping gRPC errors to gateway error codes

#### Gin HTTP Handling
- Binding and validation (`ShouldBindJSON`)
- Consistent error responses via `result.Fail` / `result.Success`

#### Redis Patterns
- TTL and key design
- Atomic operations (Lua where needed)
- Rate limit counters (per email / per IP)

#### GORM & SQL
- Basic CRUD, query building, transactions
- Error mapping for `record not found` and unique conflicts

#### Documentation Hygiene
- Update `doc/user_doc` when endpoints or paths change.
- Keep paths consistent with gateway routes.

### 5. Example: Adding a New Auth Endpoint
1. Define request/response in `apps/user/pb/auth_service.proto`.
2. Implement in `apps/user/internal/service/auth_service.go`.
3. Add repository methods if needed in `apps/user/internal/repository`.
4. Expose gRPC in gateway service layer.
5. Add DTOs and converters in `apps/gateway/internal/dto`.
6. Add route and handler in `apps/gateway/internal/router`.
7. Update documentation in `doc/user_doc`.

### 6. Security Checklist
- Never log sensitive fields (passwords, verify codes).
- Delete verification codes after successful validation.
- Validate email/phone formats before DB or Redis operations.
- Rate limit verification code sending and checking.

### 7. Minimal Change Workflow (Recommended)
1. Read existing handler/service for the same module.
2. Update proto + regenerate code if schema changes.
3. Implement user service business logic and repo changes.
4. Expose gateway handler/service + DTO conversions.
5. Update docs and ensure route paths match.
6. Run lints on edited files.

### 8. Service Decoupling Guidelines

#### 8.1 User Service Domain Boundaries
The `apps/user/` service currently contains two major domains that should remain loosely coupled:

**Core User Domain** (Authentication & User Management):
- `internal/service/auth_service.go` - Authentication (login, register, logout)
- `internal/service/user_service.go` - User profile management
- `internal/service/device_service.go` - Device session management
- `internal/repository/user_repository.go` - User data access
- `internal/repository/device_repository.go` - Device session storage

**Social Domain** (Relationships):
- `internal/service/friend_service.go` - Friend relationships
- `internal/service/blacklist_service.go` - Blacklist management
- `internal/repository/friend_repository.go` - Friend data access
- `internal/repository/blacklist_repository.go` - Blacklist storage

#### 8.2 Decoupling Best Practices
When developing new features:
1. **Avoid Cross-Domain Dependencies**: Core domain should NOT import Social domain code, and vice versa.
2. **Use Clear Interfaces**: Define service interfaces that can be easily extracted.
3. **Separate Data Models**: Keep friend/blacklist models independent from user models.
4. **Independent Proto Files**: Use separate .proto files for each domain.
5. **Database Considerations**: Design tables to minimize foreign key dependencies across domains.
6. **Aggregation in Gateway**: If social data needs user info, aggregate via gateway + user-service batch APIs.

#### 8.3 Future Split Considerations
When these domains are split into separate microservices:
- **auth-service**: Authentication, JWT, Device Sessions, User Profile
- **social-service**: Friends, Blacklist, (future: Groups, Contacts)
- Communication via gRPC between services
- Gateway routes to the appropriate service by endpoint
