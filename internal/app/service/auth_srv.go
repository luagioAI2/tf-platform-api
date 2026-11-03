package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/middleware"
	"tf-api/internal/app/service/repository"
	"tf-api/internal/app/utils"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

var AuthServiceSet = wire.NewSet(ProvideAuthService)

// AuthService 认证服务：注册 / 登录 / 令牌 / 验证码
type AuthService struct {
	UserRepo  *repository.UserRepository
	OAuthRepo *repository.UserOAuthRepository
	TokenRepo *repository.UserTokenRepository
	SmsRepo   *repository.SmsCodeRepository
	WalletSrv *WalletService
	WechatSrv *WechatService
	RDS       *redis.Client

	// smsSender 短信发送器（可替换为真实通道或 mock）
	smsSender SmsSender
}

// SmsSender 短信发送抽象。
//
// 生产环境接真实短信网关；开发环境用 DevSmsSender（把验证码打到日志）。
type SmsSender interface {
	Send(ctx context.Context, mobile, code, scene string) error
}

// ProvideAuthService 构造函数
func ProvideAuthService(
	userRepo *repository.UserRepository,
	oauthRepo *repository.UserOAuthRepository,
	tokenRepo *repository.UserTokenRepository,
	smsRepo *repository.SmsCodeRepository,
	walletSrv *WalletService,
	wechatSrv *WechatService,
	rds *redis.Client,
) *AuthService {
	return &AuthService{
		UserRepo:  userRepo,
		OAuthRepo: oauthRepo,
		TokenRepo: tokenRepo,
		SmsRepo:   smsRepo,
		WalletSrv: walletSrv,
		WechatSrv: wechatSrv,
		RDS:       rds,
		smsSender: &DevSmsSender{},
	}
}

// SetSmsSender 替换短信发送器（测试/生产注入）
func (s *AuthService) SetSmsSender(sender SmsSender) {
	if sender != nil {
		s.smsSender = sender
	}
}

// ============================ 验证码 ============================

// 发送频率限制
const (
	smsIntervalSeconds = 60 // 同一手机号两次发送最小间隔
	smsDailyLimit      = 10 // 同一手机号每天最多发送次数
	smsIPHourlyLimit   = 20 // 同一 IP 每小时最多发送次数
)

// SendSmsCode 发送验证码。
//
// 防护（三重）：
//  1. 手机号 60 秒间隔（Redis）
//  2. 手机号每天 10 条（Redis 计数）
//  3. IP 每小时 20 条（Redis 计数）
func (s *AuthService) SendSmsCode(ctx context.Context, req *entities.SendSmsCodeReq, ip string) error {
	mobile := normalizeMobile(req.Mobile)
	if mobile == "" {
		return apperrors.WithCode(apperrors.InvalidParam)
	}

	if err := s.checkSmsRateLimit(ctx, mobile, ip); err != nil {
		return err
	}

	code := generateNumericCode(entities.SmsCodeLength)
	now := time.Now().Unix()

	rec := &entities.SmsCode{
		Mobile:   mobile,
		CodeHash: hashSmsCode(mobile, code),
		Scene:    req.Scene,
		ExpireAt: now + entities.SmsCodeTTLSeconds,
		IP:       ip,
	}
	if err := s.SmsRepo.Create(ctx, rec); err != nil {
		logger.ZError("save sms code failed", zap.String("mobile", maskMobile(mobile)), zap.Error(err))
		return apperrors.WithCode(apperrors.SmsSendFailed)
	}

	if err := s.smsSender.Send(ctx, mobile, code, req.Scene); err != nil {
		logger.ZError("send sms failed", zap.String("mobile", maskMobile(mobile)), zap.Error(err))
		return apperrors.WithCode(apperrors.SmsSendFailed)
	}

	s.incrSmsCounters(ctx, mobile, ip)
	return nil
}

// checkSmsRateLimit 频率限制检查
func (s *AuthService) checkSmsRateLimit(ctx context.Context, mobile, ip string) error {
	if s.RDS == nil {
		return nil // Redis 不可用时降级（开发环境）
	}

	// 1. 手机号发送间隔
	intervalKey := "sms:interval:" + mobile
	if ok, err := s.RDS.SetNX(ctx, intervalKey, 1, smsIntervalSeconds*time.Second).Result(); err == nil && !ok {
		return apperrors.WithCode(apperrors.SmsSendTooOften)
	}

	// 2. 手机号日限
	dayKey := "sms:day:" + mobile
	if n, err := s.RDS.Get(ctx, dayKey).Int(); err == nil && n >= smsDailyLimit {
		return apperrors.WithCode(apperrors.SmsSendTooOften)
	}

	// 3. IP 小时限
	if ip != "" {
		ipKey := "sms:ip:" + ip
		if n, err := s.RDS.Get(ctx, ipKey).Int(); err == nil && n >= smsIPHourlyLimit {
			return apperrors.WithCode(apperrors.SmsSendTooOften)
		}
	}
	return nil
}

// incrSmsCounters 增加计数
func (s *AuthService) incrSmsCounters(ctx context.Context, mobile, ip string) {
	if s.RDS == nil {
		return
	}
	dayKey := "sms:day:" + mobile
	pipe := s.RDS.Pipeline()
	pipe.Incr(ctx, dayKey)
	pipe.Expire(ctx, dayKey, 24*time.Hour)
	if ip != "" {
		ipKey := "sms:ip:" + ip
		pipe.Incr(ctx, ipKey)
		pipe.Expire(ctx, ipKey, time.Hour)
	}
	_, _ = pipe.Exec(ctx)
}

// verifySmsCode 校验验证码。
//
// 安全：只存哈希、限制尝试次数、验证成功即标记已用。
func (s *AuthService) verifySmsCode(ctx context.Context, mobile, scene, code string) error {
	mobile = normalizeMobile(mobile)
	now := time.Now().Unix()

	rec, err := s.SmsRepo.GetLatestValid(ctx, mobile, scene, now)
	if err != nil {
		return apperrors.WithError(err)
	}
	if rec == nil {
		return apperrors.WithCode(apperrors.VerifyCodeExpired)
	}

	// 尝试次数限制
	if rec.Attempts >= entities.SmsCodeMaxAttempts {
		return apperrors.WithCode(apperrors.VerifyCodeTooMany)
	}

	if !constantTimeEqual(rec.CodeHash, hashSmsCode(mobile, code)) {
		if _, err := s.SmsRepo.IncrAttempts(ctx, rec.ID); err != nil {
			logger.ZWarn("incr sms attempts failed", zap.Error(err))
		}
		return apperrors.WithCode(apperrors.VerifyCodeInvalid)
	}

	if err := s.SmsRepo.MarkUsed(ctx, rec.ID); err != nil {
		return apperrors.WithError(err)
	}
	return nil
}

// ============================ 注册 ============================

// Register 手机号 + 密码注册。
//
// 流程：
//  1. 校验验证码
//  2. 手机号唯一性检查
//  3. 创建用户（bcrypt 密码）
//  4. 创建钱包（含注册赠送积分）
//  5. 签发令牌
func (s *AuthService) Register(ctx context.Context, req *entities.RegisterReq, ip, ua, device string) (*entities.LoginResp, error) {
	mobile := normalizeMobile(req.Mobile)
	if mobile == "" {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	if err := s.verifySmsCode(ctx, mobile, entities.SmsSceneRegister, req.Code); err != nil {
		return nil, err
	}

	exists, err := s.UserRepo.ExistsByMobile(ctx, mobile)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if exists {
		return nil, apperrors.WithCode(apperrors.MobileAlreadyReg)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = "宝贝" + mobile[len(mobile)-4:]
	}

	user := &entities.User{
		Mobile:         mobile,
		MobileVerified: true,
		Password:       string(hash),
		Nickname:       nickname,
		Status:         entities.UserStatusActive,
		RegisterIP:     ip,
		LastLoginIP:    ip,
		LastLoginAt:    time.Now().Unix(),
		Language:       "zh",
	}
	if err := s.UserRepo.Create(ctx, user); err != nil {
		// 唯一索引冲突（并发注册）
		if isDuplicateKey(err) {
			return nil, apperrors.WithCode(apperrors.MobileAlreadyReg)
		}
		return nil, apperrors.WithError(err)
	}

	// 创建钱包 + 注册赠送
	if err := s.WalletSrv.InitWallet(ctx, user.ID); err != nil {
		logger.ZError("init wallet failed", zap.Uint("uid", user.ID), zap.Error(err))
	}

	resp, err := s.issueTokens(ctx, user, ip, ua, device)
	if err != nil {
		return nil, err
	}
	resp.IsNewUser = true
	return resp, nil
}

// ============================ 游客 ============================

// guestNickname 游客账号的默认昵称。
//
// 不用「游客 8823」这种带数字的：孩子看得懂「小客人」，看不懂编号；
// 而且编号会让人以为这是个正经账号，就不去绑手机号了。
const guestNickname = "小客人"

// deviceIDRe 设备号的字符白名单。
//
// 这个值要进唯一索引、进日志、进风控，别让它带奇怪字符。客户端生成的是
// UUID（36 位、含 `-`），这里放宽到 8~64 位的字母数字与 `_` `-`。
var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// GuestLogin 游客进入：按设备号建号或取号。
//
// ## 为什么要这个东西
//
// 新用户没耐心：一进门就要手机号 + 验证码，多半直接关掉。所以先给一个
// 「不用注册也能玩」的账号；等他玩出东西了（挣到券、想兑换），再让他绑
// 手机号把进度存下来。
//
// ## 为什么认设备号，不认 IP
//
// IP 会变（切 WiFi、走 4G 基站），认 IP 等于每次换网就换一个账号。
// 设备号是客户端首次启动生成、存在本机的一个随机串。
//
// ## 为什么绑了手机号之后不清 device_id
//
// 清了的话，这台设备下次冷启动（本地 session 过期、又没登录）会再起一个
// 新账号，刚绑好的那份进度就成孤儿了 —— 用户看到的是「我的东西没了」。
// 保留 ⇒ 同一个设备号永远指向同一个 uid，绑定只是给这个 uid 加了个手机号。
//
// ## 安全边界
//
// 设备号就是这把钥匙：拿到它就能拿到这个账号。所以
//   - 客户端必须存在本机私有目录（不进备份、不进导出包）；
//   - 它不出现在任何接口响应里（`json:"-"`）；
//   - 账号一旦绑了手机号，**真正的身份**是手机号，设备号只是「这台设备
//     上次用的是谁」的便利贴；找回走手机号 + 验证码。
func (s *AuthService) GuestLogin(ctx context.Context, deviceID, ip, ua string) (*entities.LoginResp, error) {
	deviceID = strings.TrimSpace(deviceID)
	if !deviceIDRe.MatchString(deviceID) {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	user, err := s.UserRepo.GetByDeviceID(ctx, deviceID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if user != nil {
		if !user.IsActive() {
			return nil, apperrors.WithCode(apperrors.AccountDisabled)
		}
		return s.issueTokens(ctx, user, ip, ua, deviceID)
	}

	user = &entities.User{
		DeviceID:    deviceID,
		Nickname:    guestNickname,
		Status:      entities.UserStatusActive,
		RegisterIP:  ip,
		LastLoginIP: ip,
		LastLoginAt: time.Now().Unix(),
		Language:    "zh",
		IsGuest:     true,
	}
	if err := s.UserRepo.Create(ctx, user); err != nil {
		if !isDuplicateKey(err) {
			return nil, apperrors.WithError(err)
		}
		// 并发：同一个设备号两个请求同时到，另一个先建好了 —— 读回来接着用。
		// （device_id 上有唯一索引，所以这里不会出现两个账号指向同一台设备。）
		user, err = s.UserRepo.GetByDeviceID(ctx, deviceID)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		if user == nil {
			return nil, apperrors.WithCode(apperrors.OperationFailed)
		}
		return s.issueTokens(ctx, user, ip, ua, deviceID)
	}

	if err := s.WalletSrv.InitWallet(ctx, user.ID); err != nil {
		logger.ZError("init wallet failed", zap.Uint("uid", user.ID), zap.Error(err))
	}

	resp, err := s.issueTokens(ctx, user, ip, ua, deviceID)
	if err != nil {
		return nil, err
	}
	resp.IsNewUser = true
	return resp, nil
}

// ============================ 登录 ============================

// Login 手机号 + 密码登录
func (s *AuthService) Login(ctx context.Context, req *entities.LoginReq, ip, ua, device string) (*entities.LoginResp, error) {
	mobile := normalizeMobile(req.Mobile)

	user, err := s.UserRepo.GetByMobile(ctx, mobile)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if user == nil {
		return nil, apperrors.WithCode(apperrors.UserNotExist)
	}
	if !user.IsActive() {
		return nil, apperrors.WithCode(apperrors.AccountDisabled)
	}
	if user.Password == "" {
		return nil, apperrors.WithCode(apperrors.PasswordNotSet)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		return nil, apperrors.WithCode(apperrors.InvalidPassword)
	}

	return s.issueTokens(ctx, user, ip, ua, device)
}

// SmsLogin 手机号 + 验证码登录（免密）。
// 手机号未注册时自动注册（降低门槛，符合小程序习惯）。
func (s *AuthService) SmsLogin(ctx context.Context, req *entities.SmsLoginReq, ip, ua, device string) (*entities.LoginResp, error) {
	mobile := normalizeMobile(req.Mobile)

	if err := s.verifySmsCode(ctx, mobile, entities.SmsSceneLogin, req.Code); err != nil {
		return nil, err
	}

	user, err := s.UserRepo.GetByMobile(ctx, mobile)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	isNew := false
	if user == nil {
		// 自动注册
		user = &entities.User{
			Mobile:         mobile,
			MobileVerified: true,
			Nickname:       "宝贝" + mobile[len(mobile)-4:],
			Status:         entities.UserStatusActive,
			RegisterIP:     ip,
			Language:       "zh",
		}
		if err := s.UserRepo.Create(ctx, user); err != nil {
			if isDuplicateKey(err) {
				// 并发下被别人先创建，重新读
				user, err = s.UserRepo.GetByMobile(ctx, mobile)
				if err != nil || user == nil {
					return nil, apperrors.WithError(err)
				}
			} else {
				return nil, apperrors.WithError(err)
			}
		} else {
			isNew = true
			if err := s.WalletSrv.InitWallet(ctx, user.ID); err != nil {
				logger.ZError("init wallet failed", zap.Uint("uid", user.ID), zap.Error(err))
			}
		}
	}

	if !user.IsActive() {
		return nil, apperrors.WithCode(apperrors.AccountDisabled)
	}

	resp, err := s.issueTokens(ctx, user, ip, ua, device)
	if err != nil {
		return nil, err
	}
	resp.IsNewUser = isNew
	return resp, nil
}

// ============================ 微信登录 ============================

// WechatLogin 微信登录。
//
// 流程：
//  1. 用 code 换 openid/unionid（调用微信接口）
//  2. 已绑定 → 直接登录
//  3. 未绑定 → 创建新用户并绑定（手机号留空，后续可绑定）
func (s *AuthService) WechatLogin(ctx context.Context, req *entities.WechatLoginReq, ip, ua, device string) (*entities.LoginResp, error) {
	session, err := s.WechatSrv.Code2Session(ctx, req.Code)
	if err != nil {
		logger.ZWarn("wechat code2session failed", zap.Error(err))
		return nil, apperrors.WithCode(apperrors.WechatAuthFailed)
	}

	oauth, err := s.OAuthRepo.GetByProviderOpenID(ctx, entities.OAuthProviderWechat, session.OpenID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	// 已绑定：直接登录
	if oauth != nil {
		user, err := s.UserRepo.GetByID(ctx, oauth.UID)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		if user == nil {
			return nil, apperrors.WithCode(apperrors.UserNotExist)
		}
		if !user.IsActive() {
			return nil, apperrors.WithCode(apperrors.AccountDisabled)
		}
		// 更新微信资料快照
		if req.Nickname != "" || req.Avatar != "" {
			fields := map[string]interface{}{}
			if req.Nickname != "" && user.Nickname == "" {
				fields["nickname"] = req.Nickname
			}
			if req.Avatar != "" && user.Avatar == "" {
				fields["avatar"] = req.Avatar
			}
			if len(fields) > 0 {
				_ = s.UserRepo.Update(ctx, user.ID, fields)
				user, _ = s.UserRepo.GetByID(ctx, user.ID)
			}
		}
		return s.issueTokens(ctx, user, ip, ua, device)
	}

	// 未绑定：创建新用户
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = "微信用户"
	}
	user := &entities.User{
		Nickname:   nickname,
		Avatar:     req.Avatar,
		Status:     entities.UserStatusActive,
		RegisterIP: ip,
		Language:   "zh",
		IsGuest:    false,
	}
	if err := s.UserRepo.Create(ctx, user); err != nil {
		return nil, apperrors.WithError(err)
	}

	// 绑定微信
	bind := &entities.UserOAuth{
		UID:      user.ID,
		Provider: entities.OAuthProviderWechat,
		OpenID:   session.OpenID,
		UnionID:  session.UnionID,
		Nickname: nickname,
		Avatar:   req.Avatar,
	}
	if err := s.OAuthRepo.Create(ctx, bind); err != nil {
		if isDuplicateKey(err) {
			// 并发：另一个请求已绑定，回滚刚创建的用户并重试
			return nil, apperrors.WithCode(apperrors.WechatAlreadyBind)
		}
		return nil, apperrors.WithError(err)
	}

	if err := s.WalletSrv.InitWallet(ctx, user.ID); err != nil {
		logger.ZError("init wallet failed", zap.Uint("uid", user.ID), zap.Error(err))
	}

	resp, err := s.issueTokens(ctx, user, ip, ua, device)
	if err != nil {
		return nil, err
	}
	resp.IsNewUser = true
	return resp, nil
}

// BindMobile 绑定手机号（游客转正 / 微信用户补手机号）。
//
// ## 为什么是「同一个 uid」而不是合并账号
//
// 只往这一行上加 mobile / password / is_guest，积分、流水、农场、存档、
// 家长设置全都不动。所以游客绑完之后进度原地还在，不需要任何迁移 ——
// 反过来说，如果哪天要「把游客账号并进一个已存在的手机号账号」，
// 那就是另一件事（要搬两张账本 + 所有 uid 外键），别顺手做。
//
// ## 返回最新资料
//
// 客户端绑完要立刻把界面从「临时账号」切成「已保存」，多一次
// `/user/profile` 往返不值当，而且中间那一帧会闪。
func (s *AuthService) BindMobile(ctx context.Context, uid uint, req *entities.BindMobileReq) (*entities.UserInfo, error) {
	mobile := normalizeMobile(req.Mobile)

	if err := s.verifySmsCode(ctx, mobile, entities.SmsSceneBindMobile, req.Code); err != nil {
		return nil, err
	}

	// 手机号是否已被其他账号占用
	other, err := s.UserRepo.GetByMobile(ctx, mobile)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if other != nil && other.ID != uid {
		return nil, apperrors.WithCode(apperrors.MobileAlreadyReg)
	}

	fields := map[string]interface{}{
		"mobile":          mobile,
		"mobile_verified": true,
		// 绑完就不是游客了。扣券闸门、后台的游客筛选、「保存进度」提示
		// 全靠这个字段，漏了这一行 ⇒ 绑完了还是「临时账号」，用户会
		// 反复被要求绑定。
		// ⚠️ device_id **不清**：清了这台设备下次冷启动会另起一个号。
		"is_guest": false,
	}
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		fields["password"] = string(hash)
	}
	if err := s.UserRepo.Update(ctx, uid, fields); err != nil {
		return nil, apperrors.WithError(err)
	}

	user, err := s.UserRepo.GetByID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if user == nil {
		return nil, apperrors.WithCode(apperrors.UserNotExist)
	}
	return s.buildUserInfo(ctx, user), nil
}

// ============================ 令牌 ============================

// issueTokens 签发 access + refresh token
func (s *AuthService) issueTokens(ctx context.Context, user *entities.User, ip, ua, device string) (*entities.LoginResp, error) {
	cfg := config.Get().ServiceSettings

	accessTTL := time.Duration(cfg.TokenExpireHours) * time.Hour
	if accessTTL <= 0 {
		accessTTL = 2 * time.Hour
	}
	refreshTTL := time.Duration(cfg.TokenRefreshHours) * time.Hour
	if refreshTTL <= 0 {
		refreshTTL = 168 * time.Hour
	}

	jti := randomHex(16)
	accessToken, err := utils.GenerateJWT(cfg.JwtSignKey, accessTTL, fmt.Sprintf("%d", user.ID), jti)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	refreshToken := randomHex(32)
	now := time.Now().Unix()

	token := &entities.UserToken{
		UID:          user.ID,
		RefreshToken: refreshToken,
		ExpireAt:     now + int64(refreshTTL.Seconds()),
		// 设备号最长 64（游客用它认设备），而 device 列是 32 —— 不截断的话
		// 换成 MySQL 会直接写失败，SQLite 则默默存进去，两种都不好查。
		Device: truncate(device, 32),
		UserAgent:    truncate(ua, 255),
		IP:           ip,
	}
	if err := s.TokenRepo.Create(ctx, token); err != nil {
		return nil, apperrors.WithError(err)
	}

	// 更新登录信息
	_ = s.UserRepo.UpdateLoginInfo(ctx, user.ID, ip, now)

	return &entities.LoginResp{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(accessTTL.Seconds()),
		User:         s.buildUserInfo(ctx, user),
	}, nil
}

// buildUserInfo 组装对外的用户信息（不含敏感字段）。
//
// 抽出来是因为「绑定手机号」之后也要立刻回一份同样的结构，
// 两处各写一遍迟早会漏字段（比如 is_guest）。
func (s *AuthService) buildUserInfo(ctx context.Context, user *entities.User) *entities.UserInfo {
	hasWechat, _ := s.OAuthRepo.HasProvider(ctx, user.ID, entities.OAuthProviderWechat)
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
	}
}

// Refresh 用 refresh token 换新的 access token
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*entities.LoginResp, error) {
	t, err := s.TokenRepo.GetByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if t == nil {
		return nil, apperrors.WithCode(apperrors.TokenInvalid)
	}
	if t.ExpireAt < time.Now().Unix() {
		return nil, apperrors.WithCode(apperrors.TokenExpired)
	}

	user, err := s.UserRepo.GetByID(ctx, t.UID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if user == nil {
		return nil, apperrors.WithCode(apperrors.UserNotExist)
	}
	if !user.IsActive() {
		return nil, apperrors.WithCode(apperrors.AccountDisabled)
	}

	// 轮换 refresh token（旧的立即失效，防重放）
	if err := s.TokenRepo.Revoke(ctx, refreshToken); err != nil {
		logger.ZWarn("revoke old refresh token failed", zap.Error(err))
	}

	return s.issueTokens(ctx, user, t.IP, t.UserAgent, t.Device)
}

// Logout 登出：撤销 refresh token + 把 access token 的 jti 加入黑名单
func (s *AuthService) Logout(ctx context.Context, refreshToken, accessJTI string, accessTTL time.Duration) error {
	if refreshToken != "" {
		if err := s.TokenRepo.Revoke(ctx, refreshToken); err != nil {
			logger.ZWarn("revoke refresh token failed", zap.Error(err))
		}
	}
	if accessJTI != "" {
		if err := middleware.RevokeToken(ctx, s.RDS, accessJTI, accessTTL); err != nil {
			logger.ZWarn("revoke access token failed", zap.Error(err))
		}
	}
	return nil
}

// ============================ 密码 ============================

// ResetPassword 重置密码（验证码验证）
func (s *AuthService) ResetPassword(ctx context.Context, req *entities.ResetPasswordReq) error {
	mobile := normalizeMobile(req.Mobile)

	if err := s.verifySmsCode(ctx, mobile, entities.SmsSceneResetPassword, req.Code); err != nil {
		return err
	}

	user, err := s.UserRepo.GetByMobile(ctx, mobile)
	if err != nil {
		return apperrors.WithError(err)
	}
	if user == nil {
		return apperrors.WithCode(apperrors.UserNotExist)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return apperrors.WithError(err)
	}

	if err := s.UserRepo.Update(ctx, user.ID, map[string]interface{}{"password": string(hash)}); err != nil {
		return apperrors.WithError(err)
	}

	// 密码变更后踢下线所有设备
	if err := s.TokenRepo.RevokeAllByUID(ctx, user.ID); err != nil {
		logger.ZWarn("revoke all tokens failed", zap.Uint("uid", user.ID), zap.Error(err))
	}
	return nil
}

// ChangePassword 修改密码（需旧密码）
func (s *AuthService) ChangePassword(ctx context.Context, uid uint, req *entities.ChangePasswordReq) error {
	user, err := s.UserRepo.GetByID(ctx, uid)
	if err != nil {
		return apperrors.WithError(err)
	}
	if user == nil {
		return apperrors.WithCode(apperrors.UserNotExist)
	}
	if user.Password == "" {
		return apperrors.WithCode(apperrors.PasswordNotSet)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)) != nil {
		return apperrors.WithCode(apperrors.InvalidPassword)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return apperrors.WithError(err)
	}
	if err := s.UserRepo.Update(ctx, uid, map[string]interface{}{"password": string(hash)}); err != nil {
		return apperrors.WithError(err)
	}

	// 踢下线其它设备（当前设备的 token 也失效，前端需重新登录）
	return s.TokenRepo.RevokeAllByUID(ctx, uid)
}

// ============================ 辅助函数 ============================

var mobileRe = regexp.MustCompile(`^\+?[0-9]{8,20}$`)

// normalizeMobile 标准化手机号（去掉空格、横线，保留 +）
func normalizeMobile(mobile string) string {
	m := strings.TrimSpace(mobile)
	m = strings.ReplaceAll(m, " ", "")
	m = strings.ReplaceAll(m, "-", "")
	if !mobileRe.MatchString(m) {
		return ""
	}
	return m
}

// maskMobile 脱敏手机号（138****8000）
func maskMobile(mobile string) string {
	if len(mobile) <= 7 {
		return mobile
	}
	return mobile[:len(mobile)-8] + "****" + mobile[len(mobile)-4:]
}

// hashSmsCode 计算验证码哈希
func hashSmsCode(mobile, code string) string {
	h := sha256.Sum256([]byte("tf-sms:" + mobile + ":" + code))
	return hex.EncodeToString(h[:])
}

// constantTimeEqual 常量时间比较（防时序攻击）
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// generateNumericCode 生成指定位数的数字验证码
func generateNumericCode(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			buf[i] = byte('0' + i%10)
			continue
		}
		buf[i] = byte('0' + int(b[0])%10)
	}
	return string(buf)
}

// randomHex 生成随机十六进制串
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// truncate 截断字符串
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// isDuplicateKey 判断是否唯一索引冲突（MySQL / SQLite）
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "1062")
}

// ============================ 开发环境短信发送器 ============================

// DevSmsSender 开发环境短信发送器：把验证码打到日志，不真的发短信。
//
// ⚠️ 生产环境必须替换为真实通道，否则用户收不到验证码。
type DevSmsSender struct{}

func (d *DevSmsSender) Send(ctx context.Context, mobile, code, scene string) error {
	cfg := config.Get().ServiceSettings
	if cfg.IsProduction() {
		logger.ZError("DevSmsSender used in production! sms will not be delivered",
			zap.String("mobile", maskMobile(mobile)),
			zap.String("scene", scene))
		return errors.New("sms sender not configured for production")
	}
	logger.ZInfo("DEV SMS CODE",
		zap.String("mobile", maskMobile(mobile)),
		zap.String("scene", scene),
		zap.String("code", code))
	return nil
}
