package service

import (
	"ChatServer/apps/user/internal/repository"
	"ChatServer/apps/user/internal/utils"
	pb "ChatServer/apps/user/pb"
	"ChatServer/consts"
	"ChatServer/model"
	"ChatServer/pkg/logger"
	"context"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// friendServiceImpl 好友关系服务实现
type friendServiceImpl struct {
	friendRepo    repository.IFriendRepository
	applyRepo     repository.IApplyRepository
	userRepo      repository.IUserRepository
	blacklistRepo repository.IBlacklistRepository
}

// NewFriendService 创建好友服务实例
func NewFriendService(
	friendRepo repository.IFriendRepository,
	applyRepo repository.IApplyRepository,
	userRepo repository.IUserRepository,
	blacklistRepo repository.IBlacklistRepository,
) FriendService {
	return &friendServiceImpl{
		friendRepo:    friendRepo,
		applyRepo:     applyRepo,
		userRepo:      userRepo,
		blacklistRepo: blacklistRepo,
	}
}

// SearchUser 搜索用户
// 业务流程：
//  1. 从context中获取当前用户UUID
//  2. 调用userRepo搜索用户（按邮箱、昵称、UUID）
//  3. 调用friendRepo批量判断是否为好友
//  4. 非好友时脱敏邮箱
//  5. 返回搜索结果
//
// 错误码映射：
//   - codes.InvalidArgument: 关键词太短
//   - codes.Internal: 系统内部错误
func (s *friendServiceImpl) SearchUser(ctx context.Context, req *pb.SearchUserRequest) (*pb.SearchUserResponse, error) {
	// 1. 从context中获取当前用户UUID
	currentUserUUID, ok := ctx.Value("user_uuid").(string)
	if !ok || currentUserUUID == "" {
		logger.Error(ctx, "获取用户UUID失败")
		return nil, status.Error(codes.Unauthenticated, strconv.Itoa(consts.CodeUnauthorized))
	}

	// 2. 调用搜索用户
	users, total, err := s.userRepo.SearchUser(ctx, req.Keyword, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error(ctx, "搜索用户失败",
			logger.String("keyword", req.Keyword),
			logger.Int("page", int(req.Page)),
			logger.Int("page_size", int(req.PageSize)),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	if len(users) == 0 {
		// 没有搜索到结果，返回空列表
		return &pb.SearchUserResponse{
			Items: []*pb.SimpleUserItem{},
			Pagination: &pb.PaginationInfo{
				Page:       req.Page,
				PageSize:   req.PageSize,
				Total:      total,
				TotalPages: int32((total + int64(req.PageSize) - 1) / int64(req.PageSize)),
			},
		}, nil
	}

	// 3. 批量判断是否为好友（使用 Redis Set 优化）
	userUUIDs := make([]string, len(users))
	for i, user := range users {
		userUUIDs[i] = user.Uuid
	}

	friendMap, err := s.friendRepo.BatchCheckIsFriend(ctx, currentUserUUID, userUUIDs)
	if err != nil {
		logger.Error(ctx, "批量判断是否好友失败",
			logger.String("current_user_uuid", currentUserUUID),
			logger.Int("count", len(userUUIDs)),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	// 4. 构建响应（非好友时脱敏邮箱）
	items := make([]*pb.SimpleUserItem, len(users))
	for i, user := range users {
		email := user.Email
		if !friendMap[user.Uuid] && email != "" {
			// 非好友时脱敏邮箱：只显示前3位和@domain部分
			email = utils.MaskEmail(email)
		}

		items[i] = &pb.SimpleUserItem{
			Uuid:      user.Uuid,
			Nickname:  user.Nickname,
			Email:     email,
			Avatar:    user.Avatar,
			Signature: user.Signature,
			IsFriend:  friendMap[user.Uuid],
		}
	}

	// 5. 计算总页数
	totalPages := int32((total + int64(req.PageSize) - 1) / int64(req.PageSize))

	logger.Info(ctx, "搜索用户成功",
		logger.String("keyword", req.Keyword),
		logger.Int("page", int(req.Page)),
		logger.Int("page_size", int(req.PageSize)),
		logger.Int64("total", total),
		logger.Int("found", len(users)),
	)

	// 6. 返回搜索结果
	return &pb.SearchUserResponse{
		Items: items,
		Pagination: &pb.PaginationInfo{
			Page:       req.Page,
			PageSize:   req.PageSize,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

// SendFriendApply 发送好友申请
// 业务流程：
//  1. 从context中获取当前用户UUID（申请人）
//  2. 检查目标用户是否存在
//  3. 检查不能添加自己为好友
//  4. 检查是否已经是好友
//  5. 检查是否存在待处理的申请
//  6. 检查对方是否已将你拉黑
//  7. 检查你是否已将对方拉黑
//  8. 创建好友申请记录
//  9. 返回申请ID
//
// 错误码映射：
//   - codes.InvalidArgument: 不能添加自己为好友
//   - codes.NotFound: 用户不存在
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

	// 2. 检查目标用户是否存在
	targetUser, err := s.userRepo.GetByUUID(ctx, req.TargetUuid)
	if err != nil {
		logger.Error(ctx, "查询目标用户失败",
			logger.String("target_uuid", req.TargetUuid),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}
	if targetUser == nil {
		logger.Warn(ctx, "目标用户不存在",
			logger.String("target_uuid", req.TargetUuid),
		)
		return nil, status.Error(codes.NotFound, strconv.Itoa(consts.CodeUserNotFound))
	}

	// 3. 检查不能添加自己为好友
	if currentUserUUID == req.TargetUuid {
		logger.Warn(ctx, "不能添加自己为好友",
			logger.String("user_uuid", currentUserUUID),
		)
		return nil, status.Error(codes.InvalidArgument, strconv.Itoa(consts.CodeCannotAddSelf))
	}

	// 4. 检查是否已经是好友
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

	// 5. 检查是否存在待处理的申请
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

	// 6. 检查对方是否已将你拉黑
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

	// 7. 检查你是否已将对方拉黑
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

	// 8. 创建好友申请记录
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

	// 9. 返回申请ID
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

	// 去重收集申请人 UUID，减少批量查询压力
	applicantSet := make(map[string]struct{}, len(applies))
	for _, apply := range applies {
		if apply != nil && apply.ApplicantUuid != "" {
			applicantSet[apply.ApplicantUuid] = struct{}{}
		}
	}

	// 构造去重后的 UUID 列表
	applicantUUIDs := make([]string, 0, len(applicantSet))
	for uuid := range applicantSet {
		applicantUUIDs = append(applicantUUIDs, uuid)
	}

	// 批量查询申请人信息（昵称、头像）
	users, err := s.userRepo.BatchGetByUUIDs(ctx, applicantUUIDs)
	if err != nil {
		logger.Error(ctx, "批量查询申请人信息失败",
			logger.Int("count", len(applicantUUIDs)),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	// 将用户信息映射到 map，便于组装响应
	userMap := make(map[string]*model.UserInfo, len(users))
	for _, user := range users {
		if user != nil {
			userMap[user.Uuid] = user
		}
	}

	// 组装返回项（申请记录 + 申请人简要信息）
	items := make([]*pb.FriendApplyItem, 0, len(applies))
	for _, apply := range applies {
		if apply == nil {
			continue
		}

		user, ok := userMap[apply.ApplicantUuid]
		applicantInfo := &pb.SimpleUserInfo{
			Uuid: apply.ApplicantUuid,
		}
		if ok {
			applicantInfo.Nickname = user.Nickname
			applicantInfo.Avatar = user.Avatar
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

	// 去重收集目标用户 UUID，减少批量查询压力
	targetSet := make(map[string]struct{}, len(applies))
	for _, apply := range applies {
		if apply != nil && apply.TargetUuid != "" {
			targetSet[apply.TargetUuid] = struct{}{}
		}
	}

	// 构造去重后的 UUID 列表
	targetUUIDs := make([]string, 0, len(targetSet))
	for uuid := range targetSet {
		targetUUIDs = append(targetUUIDs, uuid)
	}

	// 批量查询目标用户信息（昵称、头像）
	users, err := s.userRepo.BatchGetByUUIDs(ctx, targetUUIDs)
	if err != nil {
		logger.Error(ctx, "批量查询目标用户信息失败",
			logger.Int("count", len(targetUUIDs)),
			logger.ErrorField("error", err),
		)
		return nil, status.Error(codes.Internal, strconv.Itoa(consts.CodeInternalError))
	}

	// 将用户信息映射到 map，便于组装响应
	userMap := make(map[string]*model.UserInfo, len(users))
	for _, user := range users {
		if user != nil {
			userMap[user.Uuid] = user
		}
	}

	// 组装返回项（申请记录 + 目标用户简要信息）
	items := make([]*pb.SentApplyItem, 0, len(applies))
	for _, apply := range applies {
		if apply == nil {
			continue
		}

		user, ok := userMap[apply.TargetUuid]
		targetInfo := &pb.SimpleUserInfo{
			Uuid: apply.TargetUuid,
		}
		if ok {
			targetInfo.Nickname = user.Nickname
			targetInfo.Avatar = user.Avatar
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
func (s *friendServiceImpl) HandleFriendApply(ctx context.Context, req *pb.HandleFriendApplyRequest) error {
	return status.Error(codes.Unimplemented, "处理好友申请功能暂未实现")
}

// GetUnreadApplyCount 获取未读申请数量
func (s *friendServiceImpl) GetUnreadApplyCount(ctx context.Context, req *pb.GetUnreadApplyCountRequest) (*pb.GetUnreadApplyCountResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取未读申请数量功能暂未实现")
}

// MarkApplyAsRead 标记申请已读
func (s *friendServiceImpl) MarkApplyAsRead(ctx context.Context, req *pb.MarkApplyAsReadRequest) error {
	return status.Error(codes.Unimplemented, "标记申请已读功能暂未实现")
}

// GetFriendList 获取好友列表
func (s *friendServiceImpl) GetFriendList(ctx context.Context, req *pb.GetFriendListRequest) (*pb.GetFriendListResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取好友列表功能暂未实现")
}

// SyncFriendList 好友增量同步
func (s *friendServiceImpl) SyncFriendList(ctx context.Context, req *pb.SyncFriendListRequest) (*pb.SyncFriendListResponse, error) {
	return nil, status.Error(codes.Unimplemented, "好友增量同步功能暂未实现")
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

// GetRelationStatus 获取关系状态
func (s *friendServiceImpl) GetRelationStatus(ctx context.Context, req *pb.GetRelationStatusRequest) (*pb.GetRelationStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取关系状态功能暂未实现")
}
