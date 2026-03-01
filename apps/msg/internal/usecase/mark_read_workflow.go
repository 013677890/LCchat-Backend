package usecase

import (
	convsvc "github.com/013677890/LCchat-Backend/apps/msg/internal/domain/conversation"
	"github.com/013677890/LCchat-Backend/apps/msg/mq"
)

// MarkReadWorkflow 标记已读用例（协调层）
//
// 编排步骤：
//  1. conversation.Service → 更新 read_seq = max(DB.read_seq, req.read_seq)
//  2. conversation.Service → 计算 unread_count = max(0, max_seq - read_seq)
//  3. mq.Producer → 写 Kafka MsgPushEvent{type="MSG_MARK_READ", data=MarkReadNotice}（多端同步）
//
// 之所以放在 usecase 而不是 conversation.Service：
// - 标记已读需要写 Kafka 做多端同步（跨领域 side effect）
type MarkReadWorkflow struct {
	convService *convsvc.Service
	producer    *mq.Producer
}

// NewMarkReadWorkflow 创建标记已读用例
func NewMarkReadWorkflow(
	convService *convsvc.Service,
	producer *mq.Producer,
) *MarkReadWorkflow {
	return &MarkReadWorkflow{
		convService: convService,
		producer:    producer,
	}
}

// TODO: 实现 Execute(ctx context.Context, req *pb.MarkReadRequest) (*pb.MarkReadResponse, error)
// 等 protoc 重跑生成 MsgPushEvent 后再实现 Kafka 投递部分
