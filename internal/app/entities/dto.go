package entities

// ============================ 注册 / 登录请求 ============================

// RegisterReq 手机号注册
type RegisterReq struct {
	Mobile   string `json:"mobile" binding:"required,min=8,max=24"`   // 手机号（含国家码）
	Password string `json:"password" binding:"required,min=6,max=64"` // 密码
	Code     string `json:"code" binding:"required,len=6,numeric"`    // 短信验证码
	Nickname string `json:"nickname" binding:"omitempty,max=32"`      // 昵称（可选，默认用手机号后 4 位）
}

// LoginReq 手机号 + 密码登录
type LoginReq struct {
	Mobile   string `json:"mobile" binding:"required,min=8,max=24"`
	Password string `json:"password" binding:"required,min=6,max=64"`
}

// SmsLoginReq 手机号 + 验证码登录（免密）
type SmsLoginReq struct {
	Mobile string `json:"mobile" binding:"required,min=8,max=24"`
	Code   string `json:"code" binding:"required,len=6,numeric"`
}

// SendSmsCodeReq 发送验证码
type SendSmsCodeReq struct {
	Mobile string `json:"mobile" binding:"required,min=8,max=24"`
	// Scene register / login / reset_password / bind_mobile
	Scene string `json:"scene" binding:"required,oneof=register login reset_password bind_mobile"`
}

// WechatLoginReq 微信登录
type WechatLoginReq struct {
	// Code 微信登录返回的临时凭证（前端 wx.login 得到）
	Code string `json:"code" binding:"required"`
	// Nickname / Avatar 前端可传（微信新版不再返回昵称头像）
	Nickname string `json:"nickname" binding:"omitempty,max=32"`
	Avatar   string `json:"avatar" binding:"omitempty,max=512"`
}

// BindMobileReq 微信账号绑定手机号
type BindMobileReq struct {
	Mobile   string `json:"mobile" binding:"required,min=8,max=24"`
	Code     string `json:"code" binding:"required,len=6,numeric"`
	Password string `json:"password" binding:"omitempty,min=6,max=64"`
}

// ResetPasswordReq 重置密码
type ResetPasswordReq struct {
	Mobile      string `json:"mobile" binding:"required,min=8,max=24"`
	Code        string `json:"code" binding:"required,len=6,numeric"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=64"`
}

// ChangePasswordReq 修改密码
type ChangePasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=64"`
}

// RefreshTokenReq 刷新令牌
type RefreshTokenReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// UpdateProfileReq 更新资料
type UpdateProfileReq struct {
	Nickname *string `json:"nickname" binding:"omitempty,max=32"`
	Avatar   *string `json:"avatar" binding:"omitempty,max=512"`
	Gender   *uint8  `json:"gender" binding:"omitempty,oneof=0 1 2"`
	Language *string `json:"language" binding:"omitempty,oneof=zh en es"`
}

// ============================ 响应 ============================

// LoginResp 登录/注册响应
type LoginResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// ExpiresIn access token 有效期（秒）
	ExpiresIn int64 `json:"expires_in"`
	// User 用户信息
	User *UserInfo `json:"user"`
	// IsNewUser 是否新注册（前端可据此展示新手引导）
	IsNewUser bool `json:"is_new_user"`
}

// UserInfo 对外的用户信息（不含敏感字段）
type UserInfo struct {
	UID            uint   `json:"uid"`
	Username       string `json:"username"`
	Mobile         string `json:"mobile"`
	MobileVerified bool   `json:"mobile_verified"`
	Nickname       string `json:"nickname"`
	Avatar         string `json:"avatar"`
	Gender         uint8  `json:"gender"`
	Language       string `json:"language"`
	IsGuest        bool   `json:"is_guest"`
	// HasPassword 是否设置过密码（微信用户可能没有）
	HasPassword bool `json:"has_password"`
	// HasWechat 是否绑定微信
	HasWechat bool  `json:"has_wechat"`
	CreatedAt int64 `json:"created_at"`
}

// WalletInfo 钱包信息
type WalletInfo struct {
	UID     uint  `json:"uid"`
	Points  int64 `json:"points"`  // 积分
	Coupon  int64 `json:"coupon"`  // 兑换券
	Diamond int64 `json:"diamond"` // 钻石
}

// ============================ 游戏 ============================

// GameListReq 游戏列表请求
type GameListReq struct {
	Paginator
	// Kind 按玩法分类筛选（可选）
	Kind string `json:"kind" binding:"omitempty"`
	// Keyword 关键词搜索（可选）
	Keyword string `json:"keyword" binding:"omitempty,max=32"`
	// OnlyOwned 只看已拥有的
	OnlyOwned bool `json:"only_owned"`
}

// GameItem 游戏列表项
type GameItem struct {
	GameID      string `json:"game_id"`
	Name        string `json:"name"`
	Emoji       string `json:"emoji"`
	Tagline     string `json:"tagline"`
	Kind        string `json:"kind"`
	Tint        string `json:"tint"`
	Status      string `json:"status"`
	CoverURL    string `json:"cover_url"`
	AgeHint     string `json:"age_hint"`
	SessionHint string `json:"session_hint"`

	PricePoints  int64 `json:"price_points"`
	PriceDiamond int64 `json:"price_diamond"`

	// Owned 当前用户是否拥有
	Owned bool `json:"owned"`
	// CanAfford 余额是否够买（已拥有时为 true）
	CanAfford bool `json:"can_afford"`
}

// GameDetail 游戏详情
type GameDetail struct {
	GameItem
	Learn    []string `json:"learn"`
	About    []string `json:"about"`
	Shots    []Shot   `json:"shots"`
	EntryURL string   `json:"entry_url"`
}

// PurchaseGameReq 购买游戏
type PurchaseGameReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	// Currency 支付币种：points / diamond（不传则自动选择）
	Currency Currency `json:"currency" binding:"omitempty,oneof=points diamond"`
	// ClientRequestID 客户端幂等键（建议传 UUID）
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// PurchaseGameResp 购买结果
type PurchaseGameResp struct {
	OrderID  string     `json:"order_id"`
	GameID   string     `json:"game_id"`
	Currency Currency   `json:"currency"`
	Amount   int64      `json:"amount"`
	Wallet   WalletInfo `json:"wallet"`
	// AlreadyOwned 是否本来就已拥有（幂等命中）
	AlreadyOwned bool `json:"already_owned"`
}

// ============================ 账本 ============================

// LedgerListReq 账本查询
type LedgerListReq struct {
	Paginator
	// Currency 币种筛选（不传则全部）
	Currency Currency `json:"currency" binding:"omitempty,oneof=points coupon diamond"`
	// Source 来源筛选（可选）
	Source string `json:"source" binding:"omitempty"`
}

// LedgerItem 账本条目
type LedgerItem struct {
	ID           uint     `json:"id"`
	Currency     Currency `json:"currency"`
	Delta        int64    `json:"delta"`
	BalanceAfter int64    `json:"balance_after"`
	Source       string   `json:"source"`
	RefID        string   `json:"ref_id"`
	Memo         string   `json:"memo"`
	CreatedAt    int64    `json:"created_at"`
}
