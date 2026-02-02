package service

import (
	"ChatServer/apps/user/internal/repository"
	pb "ChatServer/apps/user/pb"
	"ChatServer/consts"
	"ChatServer/model"
	"ChatServer/pkg/logger"
	"context"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// friendServiceImpl 好友关系服务实现
type friendServiceImpl struct {
	friendRepo    repository.IFriendRepository
	applyRepo     repository.IApplyRepository
	blacklistRepo repository.IBlacklistRepository
}

// NewFriendService 创建好友服务实例
func NewFriendService(
	friendRepo repository.IFriendRepository,
	applyRepo repository.IApplyRepository,
	blacklistRepo repository.IBlacklistRepository,
) FriendService {
	return &friendServiceImpl{
		friendRepo:    friendRepo,
		applyRepo:     applyRepo,
		blacklistRepo: blacklistRepo,
	}
}

// SendFriendApply 发送好友申请
// 业务流程：
//  1. 从context中获取当前用户UUID（申请人）
//  2. 检查不能添加自己为好友
//  3. 检查是否已经是好友
//  4. 检查是否存在待处理的申请
//  5. 检查对方是否已将你拉黑
//  6. 检查你是否已将对方拉黑
//  7. 创建好友申请记录
//  8. 返回申请ID
//
// 错误码映射：
//   - codes.InvalidArgument: 不能添加自己为好友
//   - codes.AlreadyExists: 已经是好友、申请已发送
//   - codes.FailedPrecondition: 对方已将你拉黑、你已将对方拉黑
//   - codes.Internal: 系统内部错误
func (s *friendServiceImpl) SendFriendApply(ctx context.Context, req *pb.SendFriendApplyRequest) (*pb.SendFriendApplyResponse, error) {
	// 1. 从context中获取当前用户UUID（申请人）
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 检查不能添加自己为好友
	if currentUserUUID == req.TargetUuid {
		logger.Warn(ctx, "不能添加自己为好友",
			logger.String("user_uuid", currentUserUUID),
		)
		return nil, status.Error(codes.InvalidArgument, strconv.Itoa(consts.CodeCannotAddSelf))
	}

	// 3. 检查是否已经是好友
	isFriend, err := s.friendRepo.IsFriend(ctx, currentUserUUID, req.TargetUuid)
	if err != nil {
		logger.Error(ctx, "检查是否好友失败",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if isFriend {
		logger.Info(ctx, "已经是好友",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
		)
		return nil, status.Error(codes.AlreadyExists, strconv.Itoa(consts.CodeAlreadyFriend))
	}

	// 4. 检查是否存在待处理的申请
	exists, err := s.applyRepo.ExistsPendingRequest(ctx, currentUserUUID, req.TargetUuid)
	if err != nil {
		logger.Error(ctx, "检查待处理申请失败",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if exists {
		logger.Info(ctx, "好友申请已发送",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
		)
		return nil, status.Error(codes.AlreadyExists, strconv.Itoa(consts.CodeFriendRequestSent))
	}

	// 5. 检查对方是否已将你拉黑
	isBlockedByTarget, err := s.blacklistRepo.IsBlocked(ctx, req.TargetUuid, currentUserUUID)
	if err != nil {
		logger.Error(ctx, "检查是否被拉黑失败",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if isBlockedByTarget {
		logger.Info(ctx, "对方已将你拉黑",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
		)
		return nil, status.Error(codes.FailedPrecondition, strconv.Itoa(consts.CodePeerBlacklistYou))
	}

	// 6. 检查你是否已将对方拉黑
	isBlocked, err := s.blacklistRepo.IsBlocked(ctx, currentUserUUID, req.TargetUuid)
	if err != nil {
		logger.Error(ctx, "检查拉黑状态失败",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if isBlocked {
		logger.Info(ctx, "你已将对方拉黑",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
		)
		return nil, status.Error(codes.FailedPrecondition, strconv.Itoa(consts.CodeYouBlacklistPeer))
	}

	// 7. 创建好友申请记录
	apply := &model.ApplyRequest{
		ApplyType:     0, // 0=好友申请
		ApplicantUuid: currentUserUUID,
		TargetUuid:    req.TargetUuid,
		Status:        0, // 0=待处理
		IsRead:        false,
		Reason:        req.Reason,
		Source:        req.Source,
	}

	createdApply, err := s.applyRepo.Create(ctx, apply)
	if err != nil {
		logger.Error(ctx, "创建好友申请失败",
			logger.String("user_uuid", currentUserUUID),
			logger.String("target_uuid", req.TargetUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	logger.Info(ctx, "发送好友申请成功",
		logger.String("user_uuid", currentUserUUID),
		logger.String("target_uuid", req.TargetUuid),
		logger.Int64("apply_id", createdApply.Id),
		logger.String("reason", req.Reason),
		logger.String("source", req.Source),
	)

	// 8. 返回申请ID
	return &pb.SendFriendApplyResponse{
		ApplyId: createdApply.Id,
	}, nil
}

// GetFriendApplyList 获取好友申请列表
func (s *friendServiceImpl) GetFriendApplyList(ctx context.Context, req *pb.GetFriendApplyListRequest) (*pb.GetFriendApplyListResponse, error) {
	// 从上下文获取当前用户
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 兜底分页参数（即使网关做了默认值，这里也防御性处理）
	page := req.Page
	pageSize := req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	// 查询申请列表（status<0 表示全部状态）
	applies, total, err := s.applyRepo.GetPendingList(ctx, currentUserUUID, int(req.Status), int(page), int(pageSize))
	if err != nil {
		logger.Error(ctx, "获取好友申请列表失败",
			logger.String("user_uuid", currentUserUUID),
			logger.Int32("status", req.Status),
			logger.Int32("page", page),
			logger.Int32("page_size", pageSize),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if len(applies) == 0 {
		// 空列表也需要清除未读数量红点（尽力而为）
		if err := s.applyRepo.ClearUnreadCount(ctx, currentUserUUID); err != nil {
			logger.Warn(ctx, "清除好友申请未读数量失败",
				logger.String("user_uuid", currentUserUUID),
				logger.ErrorField("error", err),
			)
		}
		// 空列表直接返回，避免后续无意义的批量查询
		return &pb.GetFriendApplyListResponse{
			Items: []*pb.FriendApplyItem{},
			Pagination: &pb.PaginationInfo{
				Page:       page,
				PageSize:   pageSize,
				Total:      total,
				TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
			},
		}, nil
	}

	// 组装返回项（申请记录 + 申请人简要信息）
	items := make([]*pb.FriendApplyItem, 0, len(applies))
	unreadIDs := make([]int64, 0) // 收集未读申请的 ID

	for _, apply := range applies {
		if apply == nil {
			continue
		}

		// 收集未读申请 ID
		if !apply.IsRead {
			unreadIDs = append(unreadIDs, apply.Id)
		}

		applicantInfo := &pb.SimpleUserInfo{
			Uuid: apply.ApplicantUuid,
		}

		// created_at 使用毫秒时间戳（与网关 DTO 一致）
		items = append(items, &pb.FriendApplyItem{
			ApplyId:       apply.Id,
			ApplicantUuid: apply.ApplicantUuid,
			ApplicantInfo: applicantInfo,
			Reason:        apply.Reason,
			Source:        apply.Source,
			Status:        int32(apply.Status),
			IsRead:        apply.IsRead,
			CreatedAt:     apply.CreatedAt.UnixMilli(),
		})
	}

	// 异步标记已读（不阻塞响应）
	if len(unreadIDs) > 0 {
		s.applyRepo.MarkAsReadAsync(ctx, unreadIDs)
	}

	// 清除未读数量红点（尽力而为）
	if err := s.applyRepo.ClearUnreadCount(ctx, currentUserUUID); err != nil {
		logger.Warn(ctx, "清除好友申请未读数量失败",
			logger.String("user_uuid", currentUserUUID),
			logger.ErrorField("error", err),
		)
	}

	return &pb.GetFriendApplyListResponse{
		Items: items,
		Pagination: &pb.PaginationInfo{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
		},
	}, nil
}

// GetSentApplyList 获取发出的申请列表
func (s *friendServiceImpl) GetSentApplyList(ctx context.Context, req *pb.GetSentApplyListRequest) (*pb.GetSentApplyListResponse, error) {
	// 从上下文获取当前用户（申请人）
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 兜底分页参数（即使网关做了默认值，这里也防御性处理）
	page := req.Page
	pageSize := req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	// 查询发出的申请列表（status<0 表示全部状态）
	applies, total, err := s.applyRepo.GetSentList(ctx, currentUserUUID, int(req.Status), int(page), int(pageSize))
	if err != nil {
		logger.Error(ctx, "获取发出的申请列表失败",
			logger.String("user_uuid", currentUserUUID),
			logger.Int32("status", req.Status),
			logger.Int32("page", page),
			logger.Int32("page_size", pageSize),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if len(applies) == 0 {
		// 空列表直接返回，避免后续无意义的批量查询
		return &pb.GetSentApplyListResponse{
			Items: []*pb.SentApplyItem{},
			Pagination: &pb.PaginationInfo{
				Page:       page,
				PageSize:   pageSize,
				Total:      total,
				TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
			},
		}, nil
	}

	// 组装返回项（申请记录 + 目标用户简要信息）
	items := make([]*pb.SentApplyItem, 0, len(applies))
	for _, apply := range applies {
		if apply == nil {
			continue
		}

		targetInfo := &pb.SimpleUserInfo{
			Uuid: apply.TargetUuid,
		}

		items = append(items, &pb.SentApplyItem{
			ApplyId:    apply.Id,
			TargetUuid: apply.TargetUuid,
			TargetInfo: targetInfo,
			Reason:     apply.Reason,
			Source:     apply.Source,
			Status:     int32(apply.Status),
			IsRead:     apply.IsRead,
			CreatedAt:  apply.CreatedAt.UnixMilli(),
		})
	}

	return &pb.GetSentApplyListResponse{
		Items: items,
		Pagination: &pb.PaginationInfo{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
		},
	}, nil
}

// HandleFriendApply 处理好友申请
// 业务流程：
//  1. 从context获取当前用户UUID
//  2. 根据applyId获取申请详情
//  3. 验证当前用户是否为申请的目标用户（有权限处理）
//  4. 同意：调用 AcceptApplyAndCreateRelation（事务 + CAS幂等）
//     拒绝：调用 UpdateStatus（CAS幂等）
func (s *friendServiceImpl) HandleFriendApply(ctx context.Context, req *pb.HandleFriendApplyRequest) error {
	// 1. 从context获取当前用户UUID（处理人）
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 根据applyId获取申请详情
	apply, err := s.applyRepo.GetByID(ctx, req.ApplyId)
	if err != nil {
		logger.Warn(ctx, "获取好友申请失败",
			logger.Int64("apply_id", req.ApplyId),
			logger.ErrorField("error", err),
		)
		return status.Error(codes.NotFound, strconv.Itoa(consts.CodeApplyNotFoundOrHandle))
	}

	// 3. 验证当前用户是否有权限处理该申请
	if apply.TargetUuid != currentUserUUID {
		logger.Warn(ctx, "无权限处理该申请",
			logger.Int64("apply_id", req.ApplyId),
			logger.String("target_uuid", apply.TargetUuid),
			logger.String("current_user", currentUserUUID),
		)
		return status.Error(codes.PermissionDenied, strconv.Itoa(consts.CodeNoPermission))
	}

	// 4. 处理申请
	if req.Action == 1 {
		// 同意：事务性更新申请状态 + 创建好友关系
		alreadyProcessed, err := s.applyRepo.AcceptApplyAndCreateRelation(ctx, req.ApplyId, currentUserUUID, apply.ApplicantUuid, req.Remark)
		if err != nil {
			logger.Error(ctx, "同意好友申请失败",
				logger.Int64("apply_id", req.ApplyId),
				logger.ErrorField("error", err),
			)
			return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
		}

		if alreadyProcessed {
			logger.Info(ctx, "申请已被处理（幂等成功）",
				logger.Int64("apply_id", req.ApplyId),
			)
		} else {
			logger.Info(ctx, "同意好友申请，创建好友关系成功",
				logger.String("user_uuid", currentUserUUID),
				logger.String("friend_uuid", apply.ApplicantUuid),
				logger.Int64("apply_id", req.ApplyId),
			)
		}
	} else {
		// 拒绝：只更新申请状态
		err = s.applyRepo.UpdateStatus(ctx, req.ApplyId, int(req.Action), req.Remark)
		if err != nil {
			// ErrApplyNotFound 也是幂等成功
			if err == repository.ErrApplyNotFound {
				logger.Info(ctx, "申请已被处理（幂等成功）",
					logger.Int64("apply_id", req.ApplyId),
				)
				return nil
			}
			logger.Error(ctx, "拒绝好友申请失败",
				logger.Int64("apply_id", req.ApplyId),
				logger.ErrorField("error", err),
			)
			return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
		}

		logger.Info(ctx, "拒绝好友申请",
			logger.String("user_uuid", currentUserUUID),
			logger.String("applicant_uuid", apply.ApplicantUuid),
			logger.Int64("apply_id", req.ApplyId),
		)
	}

	return nil
}

// GetUnreadApplyCount 获取未读申请数量
func (s *friendServiceImpl) GetUnreadApplyCount(ctx context.Context, req *pb.GetUnreadApplyCountRequest) (*pb.GetUnreadApplyCountResponse, error) {
	// 1. 获取当前用户 UUID
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 只读 Redis 未读数量（不命中直接返回 0）
	count, err := s.applyRepo.GetUnreadCount(ctx, currentUserUUID)
	if err != nil {
		logger.Warn(ctx, "获取好友申请未读数量失败，降级返回 0",
			logger.String("user_uuid", currentUserUUID),
			logger.ErrorField("error", err),
		)
		count = 0
	}

	return &pb.GetUnreadApplyCountResponse{
		UnreadCount: int32(count),
	}, nil
}

// MarkApplyAsRead 标记申请已读
func (s *friendServiceImpl) MarkApplyAsRead(ctx context.Context, req *pb.MarkApplyAsReadRequest) error {
	// 1. 获取当前用户 UUID
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 标记已读（applyIds 为空则标记全部）
	if len(req.ApplyIds) == 0 {
		if _, err := s.applyRepo.MarkAllAsRead(ctx, currentUserUUID); err != nil {
			logger.Error(ctx, "标记全部申请已读失败",
				logger.String("user_uuid", currentUserUUID),
				logger.ErrorField("error", err),
			)
			return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
		}
	} else {
		if _, err := s.applyRepo.MarkAsRead(ctx, currentUserUUID, req.ApplyIds); err != nil {
			logger.Error(ctx, "标记申请已读失败",
				logger.String("user_uuid", currentUserUUID),
				logger.Int("count", len(req.ApplyIds)),
				logger.ErrorField("error", err),
			)
			return status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
		}
	}

	// 3. 清除未读数量红点（尽力而为）
	if err := s.applyRepo.ClearUnreadCount(ctx, currentUserUUID); err != nil {
		logger.Warn(ctx, "清除好友申请未读数量失败",
			logger.String("user_uuid", currentUserUUID),
			logger.ErrorField("error", err),
		)
	}

	return nil
}

// GetFriendList 获取好友列表
func (s *friendServiceImpl) GetFriendList(ctx context.Context, req *pb.GetFriendListRequest) (*pb.GetFriendListResponse, error) {
	// 1. 从context中获取当前用户UUID
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 兜底分页参数（即使网关做了默认值，这里也防御性处理）
	page := req.Page
	pageSize := req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	// 3. 获取好友关系列表
	relations, total, version, err := s.friendRepo.GetFriendList(ctx, currentUserUUID, req.GroupTag, int(page), int(pageSize))
	if err != nil {
		logger.Error(ctx, "获取好友列表失败",
			logger.String("user_uuid", currentUserUUID),
			logger.String("group_tag", req.GroupTag),
			logger.Int32("page", page),
			logger.Int32("page_size", pageSize),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if len(relations) == 0 {
		return &pb.GetFriendListResponse{
			Items: []*pb.FriendItem{},
			Pagination: &pb.PaginationInfo{
				Page:       page,
				PageSize:   pageSize,
				Total:      total,
				TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
			},
			Version: version,
		}, nil
	}

	// 4. 组装返回项（好友关系数据）
	items := make([]*pb.FriendItem, 0, len(relations))
	for _, relation := range relations {
		if relation == nil {
			continue
		}

		item := &pb.FriendItem{
			Uuid:      relation.PeerUuid,
			Remark:    relation.Remark,
			GroupTag:  relation.GroupTag,
			Source:    relation.Source,
			CreatedAt: relation.CreatedAt.UnixMilli(),
		}

		items = append(items, item)
	}

	return &pb.GetFriendListResponse{
		Items: items,
		Pagination: &pb.PaginationInfo{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
		},
		Version: version,
	}, nil
}

// SyncFriendList 好友增量同步
func (s *friendServiceImpl) SyncFriendList(ctx context.Context, req *pb.SyncFriendListRequest) (*pb.SyncFriendListResponse, error) {
	const syncVersionRollbackMs int64 = 2000 // 回退 2s，避免事务时间差漏数据

	// 1. 从context中获取当前用户UUID
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 兜底同步参数
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	version := req.Version
	if version < 0 {
		version = 0
	}

	// 3. 查询增量变更（按时间升序）
	relations, serverTime, hasMore, err := s.friendRepo.SyncFriendList(ctx, currentUserUUID, version, limit)
	if err != nil {
		logger.Error(ctx, "增量同步好友列表失败",
			logger.String("user_uuid", currentUserUUID),
			logger.Int("limit", limit),
			logger.Int64("version", version),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	// 4. 无变更：直接返回（latestVersion 使用服务器时间回退一小段）
	if len(relations) == 0 {
		latestVersion := serverTime - syncVersionRollbackMs
		if latestVersion < 0 {
			latestVersion = 0
		}
		return &pb.SyncFriendListResponse{
			Changes:       []*pb.FriendChange{},
			HasMore:       false,
			LatestVersion: latestVersion,
		}, nil
	}

	// 5. 判断是否还有更多
	//if len(relations) > limit {
	//	hasMore = true
	//	relations = relations[:limit]
	//}

	// 6. 组装变更列表
	versionTime := time.UnixMilli(version)
	changes := make([]*pb.FriendChange, 0, len(relations))
	var lastChangedAt int64

	for _, relation := range relations {
		if relation == nil {
			continue
		}

		changeType := "update"
		changedAt := relation.UpdatedAt.UnixMilli()

		if relation.DeletedAt.Valid {
			changeType = "delete"
			changedAt = relation.DeletedAt.Time.UnixMilli()
		} else if relation.CreatedAt.After(versionTime) {
			changeType = "add"
		}

		change := &pb.FriendChange{
			Uuid:       relation.PeerUuid,
			Remark:     relation.Remark,
			GroupTag:   relation.GroupTag,
			Source:     relation.Source,
			ChangeType: changeType,
			ChangedAt:  changedAt,
		}

		changes = append(changes, change)
		lastChangedAt = changedAt
	}

	// 7. latestVersion 规则：
	// - hasMore=true：取本批次最后一条的 changedAt
	// - hasMore=false：取服务器当前时间并回退一小段
	var latestVersion int64
	if hasMore {
		latestVersion = lastChangedAt
	} else {
		latestVersion = serverTime - syncVersionRollbackMs
		if latestVersion < 0 {
			latestVersion = 0
		}
	}

	return &pb.SyncFriendListResponse{
		Changes:       changes,
		HasMore:       hasMore,
		LatestVersion: latestVersion,
	}, nil
}

// DeleteFriend 删除好友
func (s *friendServiceImpl) DeleteFriend(ctx context.Context, req *pb.DeleteFriendRequest) error {
	return status.Error(codes.Unimplemented, "删除好友功能暂未实现")
}

// SetFriendRemark 设置好友备注
func (s *friendServiceImpl) SetFriendRemark(ctx context.Context, req *pb.SetFriendRemarkRequest) error {
	return status.Error(codes.Unimplemented, "设置好友备注功能暂未实现")
}

// SetFriendTag 设置好友标签
func (s *friendServiceImpl) SetFriendTag(ctx context.Context, req *pb.SetFriendTagRequest) error {
	return status.Error(codes.Unimplemented, "设置好友标签功能暂未实现")
}

// GetTagList 获取标签列表
func (s *friendServiceImpl) GetTagList(ctx context.Context, req *pb.GetTagListRequest) (*pb.GetTagListResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取标签列表功能暂未实现")
}

// CheckIsFriend 判断是否好友
func (s *friendServiceImpl) CheckIsFriend(ctx context.Context, req *pb.CheckIsFriendRequest) (*pb.CheckIsFriendResponse, error) {
	isFriend, err := s.friendRepo.IsFriend(ctx, req.UserUuid, req.PeerUuid)
	if err != nil {
		logger.Error(ctx, "判断是否好友失败",
			logger.String("user_uuid", req.UserUuid),
			logger.String("peer_uuid", req.PeerUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}
	return &pb.CheckIsFriendResponse{
		IsFriend: isFriend,
	}, nil
}

// BatchCheckIsFriend 批量判断是否好友
func (s *friendServiceImpl) BatchCheckIsFriend(ctx context.Context, req *pb.BatchCheckIsFriendRequest) (*pb.BatchCheckIsFriendResponse, error) {
	if req == nil || len(req.PeerUuids) == 0 {
		return &pb.BatchCheckIsFriendResponse{
			Items: []*pb.FriendCheckItem{},
		}, nil
	}

	result, err := s.friendRepo.BatchCheckIsFriend(ctx, req.UserUuid, req.PeerUuids)
	if err != nil {
		logger.Error(ctx, "批量判断是否好友失败",
			logger.String("user_uuid", req.UserUuid),
			logger.Int("count", len(req.PeerUuids)),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	items := make([]*pb.FriendCheckItem, 0, len(req.PeerUuids))
	for _, peerUUID := range req.PeerUuids {
		if peerUUID == "" {
			continue
		}
		items = append(items, &pb.FriendCheckItem{
			PeerUuid: peerUUID,
			IsFriend: result[peerUUID],
		})
	}

	return &pb.BatchCheckIsFriendResponse{
		Items: items,
	}, nil
}

// GetRelationStatus 获取关系状态
func (s *friendServiceImpl) GetRelationStatus(ctx context.Context, req *pb.GetRelationStatusRequest) (*pb.GetRelationStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取关系状态功能暂未实现")
}
