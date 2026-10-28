package service

import (
	"context"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
)

var UserServiceSet = wire.NewSet(ProvideUserService)

// UserService 用户资料服务
type UserService struct {
	Repo      *repository.UserRepository
	OAuthRepo *repository.UserOAuthRepository
}

// ProvideUserService 构造函数
func ProvideUserService(repo *repository.UserRepository, oauthRepo *repository.UserOAuthRepository) *UserService {
	return &UserService{Repo: repo, OAuthRepo: oauthRepo}
}

// GetProfile 查询用户资料
func (s *UserService) GetProfile(ctx context.Context, uid uint) (*entities.UserInfo, error) {
	user, err := s.Repo.GetByID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if user == nil {
		return nil, apperrors.WithCode(apperrors.UserNotExist)
	}

	hasWechat, err := s.OAuthRepo.HasProvider(ctx, uid, entities.OAuthProviderWechat)
	if err != nil {
		hasWechat = false
	}

	return &entities.UserInfo{
		UID:            user.ID,
		Username:       user.Username,
		Mobile:         maskMobile(user.Mobile),
		MobileVerified: user.MobileVerified,
		Nickname:       user.Nickname,
		Avatar:         user.Avatar,
		Gender:         user.Gender,
		Language:       user.Language,
		IsGuest:        user.IsGuest,
		HasPassword:    user.Password != "",
		HasWechat:      hasWechat,
		CreatedAt:      user.CreatedAt,
	}, nil
}

// UpdateProfile 更新资料（只更新传入的字段）
func (s *UserService) UpdateProfile(ctx context.Context, uid uint, req *entities.UpdateProfileReq) (*entities.UserInfo, error) {
	fields := make(map[string]interface{}, 4)

	if req.Nickname != nil {
		fields["nickname"] = *req.Nickname
	}
	if req.Avatar != nil {
		fields["avatar"] = *req.Avatar
	}
	if req.Gender != nil {
		fields["gender"] = *req.Gender
	}
	if req.Language != nil {
		fields["language"] = *req.Language
	}

	if len(fields) > 0 {
		if err := s.Repo.Update(ctx, uid, fields); err != nil {
			return nil, apperrors.WithError(err)
		}
	}
	return s.GetProfile(ctx, uid)
}
