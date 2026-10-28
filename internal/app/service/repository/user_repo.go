package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var UserRepositorySet = wire.NewSet(wire.Struct(new(UserRepository), "*"))

// UserRepository 用户数据访问
type UserRepository struct {
	DB  *gorm.DB
	RDS *redis.Client
}

// NewUserRepository 直接构造（供 seed / 脚本使用，wire 走 struct provider）
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{DB: db}
}

// Create 创建用户
func (r *UserRepository) Create(ctx context.Context, user *entities.User) error {
	return r.DB.WithContext(ctx).Create(user).Error
}

// GetByID 按 UID 查询
func (r *UserRepository) GetByID(ctx context.Context, uid uint) (*entities.User, error) {
	var u entities.User
	err := r.DB.WithContext(ctx).Where("id = ?", uid).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByMobile 按手机号查询
func (r *UserRepository) GetByMobile(ctx context.Context, mobile string) (*entities.User, error) {
	var u entities.User
	err := r.DB.WithContext(ctx).Where("mobile = ?", mobile).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByUsername 按用户名查询
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*entities.User, error) {
	var u entities.User
	err := r.DB.WithContext(ctx).Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Update 更新用户（只更新非零字段）
func (r *UserRepository) Update(ctx context.Context, uid uint, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&entities.User{}).
		Where("id = ?", uid).
		Updates(fields).Error
}

// UpdateLoginInfo 更新登录信息
func (r *UserRepository) UpdateLoginInfo(ctx context.Context, uid uint, ip string, at int64) error {
	return r.DB.WithContext(ctx).Model(&entities.User{}).
		Where("id = ?", uid).
		Updates(map[string]interface{}{
			"last_login_ip": ip,
			"last_login_at": at,
		}).Error
}

// ExistsByMobile 手机号是否已注册
func (r *UserRepository) ExistsByMobile(ctx context.Context, mobile string) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.User{}).
		Where("mobile = ?", mobile).Count(&count).Error
	return count > 0, err
}

// ============================ 第三方账号 ============================

var UserOAuthRepositorySet = wire.NewSet(wire.Struct(new(UserOAuthRepository), "*"))

// UserOAuthRepository 第三方账号数据访问
type UserOAuthRepository struct {
	DB *gorm.DB
}

// Create 创建绑定
func (r *UserOAuthRepository) Create(ctx context.Context, o *entities.UserOAuth) error {
	return r.DB.WithContext(ctx).Create(o).Error
}

// GetByProviderOpenID 按平台 + openid 查询
func (r *UserOAuthRepository) GetByProviderOpenID(ctx context.Context, provider, openID string) (*entities.UserOAuth, error) {
	var o entities.UserOAuth
	err := r.DB.WithContext(ctx).
		Where("provider = ? AND openid = ?", provider, openID).
		First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetByUIDAndProvider 按 UID + 平台查询
func (r *UserOAuthRepository) GetByUIDAndProvider(ctx context.Context, uid uint, provider string) (*entities.UserOAuth, error) {
	var o entities.UserOAuth
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND provider = ?", uid, provider).
		First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// HasProvider 用户是否绑定了某平台
func (r *UserOAuthRepository) HasProvider(ctx context.Context, uid uint, provider string) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.UserOAuth{}).
		Where("uid = ? AND provider = ?", uid, provider).Count(&count).Error
	return count > 0, err
}

// ============================ 令牌 ============================

var UserTokenRepositorySet = wire.NewSet(wire.Struct(new(UserTokenRepository), "*"))

// UserTokenRepository 登录令牌数据访问
type UserTokenRepository struct {
	DB *gorm.DB
}

// Create 保存 refresh token
func (r *UserTokenRepository) Create(ctx context.Context, t *entities.UserToken) error {
	return r.DB.WithContext(ctx).Create(t).Error
}

// GetByRefreshToken 按 refresh token 查询
func (r *UserTokenRepository) GetByRefreshToken(ctx context.Context, token string) (*entities.UserToken, error) {
	var t entities.UserToken
	err := r.DB.WithContext(ctx).
		Where("refresh_token = ? AND revoked = ?", token, false).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Revoke 撤销单个 token
func (r *UserTokenRepository) Revoke(ctx context.Context, token string) error {
	return r.DB.WithContext(ctx).Model(&entities.UserToken{}).
		Where("refresh_token = ?", token).
		Update("revoked", true).Error
}

// RevokeAllByUID 撤销用户所有 token（踢下线）
func (r *UserTokenRepository) RevokeAllByUID(ctx context.Context, uid uint) error {
	return r.DB.WithContext(ctx).Model(&entities.UserToken{}).
		Where("uid = ? AND revoked = ?", uid, false).
		Update("revoked", true).Error
}

// CleanExpired 清理过期 token（定时任务用）
func (r *UserTokenRepository) CleanExpired(ctx context.Context, before int64) (int64, error) {
	res := r.DB.WithContext(ctx).
		Where("expire_at < ?", before).
		Delete(&entities.UserToken{})
	return res.RowsAffected, res.Error
}

// ============================ 验证码 ============================

var SmsCodeRepositorySet = wire.NewSet(wire.Struct(new(SmsCodeRepository), "*"))

// SmsCodeRepository 验证码数据访问
type SmsCodeRepository struct {
	DB *gorm.DB
}

// Create 保存验证码
func (r *SmsCodeRepository) Create(ctx context.Context, c *entities.SmsCode) error {
	return r.DB.WithContext(ctx).Create(c).Error
}

// GetLatestValid 查询某手机号在某场景下最新一条未使用的验证码
func (r *SmsCodeRepository) GetLatestValid(ctx context.Context, mobile, scene string, now int64) (*entities.SmsCode, error) {
	var c entities.SmsCode
	err := r.DB.WithContext(ctx).
		Where("mobile = ? AND scene = ? AND used = ? AND expire_at > ?", mobile, scene, false, now).
		Order("id DESC").
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkUsed 标记已使用
func (r *SmsCodeRepository) MarkUsed(ctx context.Context, id uint) error {
	return r.DB.WithContext(ctx).Model(&entities.SmsCode{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"used": true}).Error
}

// IncrAttempts 增加尝试次数
func (r *SmsCodeRepository) IncrAttempts(ctx context.Context, id uint) (int, error) {
	if err := r.DB.WithContext(ctx).Model(&entities.SmsCode{}).
		Where("id = ?", id).
		UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error; err != nil {
		return 0, err
	}
	var c entities.SmsCode
	if err := r.DB.WithContext(ctx).Select("attempts").Where("id = ?", id).First(&c).Error; err != nil {
		return 0, err
	}
	return c.Attempts, nil
}

// CountRecent 统计最近一段时间内某手机号的发送次数（频率限制）
func (r *SmsCodeRepository) CountRecent(ctx context.Context, mobile string, since int64) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.SmsCode{}).
		Where("mobile = ? AND created_at >= ?", mobile, since).
		Count(&count).Error
	return count, err
}

// CleanExpired 清理过期验证码
func (r *SmsCodeRepository) CleanExpired(ctx context.Context, before int64) (int64, error) {
	res := r.DB.WithContext(ctx).Where("expire_at < ?", before).Delete(&entities.SmsCode{})
	return res.RowsAffected, res.Error
}
