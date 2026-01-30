package service

import (
	"ChatServer/apps/user/internal/repository"
	"ChatServer/apps/user/internal/utils"
	pb "ChatServer/apps/user/pb"
	"ChatServer/consts"
	"ChatServer/pkg/logger"
	"context"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// friendServiceImpl 好友关系服务实现
type friendServiceImpl struct {
	friendRepo repository.IFriendRepository
	applyRepo  repository.IApplyRepository
	userRepo   repository.IUserRepository
}

// NewFriendService 创建好友服务实例
func NewFriendService(
	friendRepo repository.IFriendRepository,
	applyRepo repository.IApplyRepository,
	userRepo repository.IUserRepository,
) FriendService {
	return &friendServiceImpl{
		friendRepo: friendRepo,
		applyRepo:  applyRepo,
		userRepo:   userRepo,
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
func (s *friendServiceImpl) SendFriendApply(ctx context.Context, req *pb.SendFriendApplyRequest) (*pb.SendFriendApplyResponse, error) {
	return nil, status.Error(codes.Unimplemented, "发送好友申请功能暂未实现")
}

// GetFriendApplyList 获取好友申请列表
func (s *friendServiceImpl) GetFriendApplyList(ctx context.Context, req *pb.GetFriendApplyListRequest) (*pb.GetFriendApplyListResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取好友申请列表功能暂未实现")
}

// GetSentApplyList 获取发出的申请列表
func (s *friendServiceImpl) GetSentApplyList(ctx context.Context, req *pb.GetSentApplyListRequest) (*pb.GetSentApplyListResponse, error) {
	return nil, status.Error(codes.Unimplemented, "获取发出的申请列表功能暂未实现")
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
