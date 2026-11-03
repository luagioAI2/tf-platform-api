package entities

// ============================ 注册 / 登录请求 ============================

// RegisterReq 手机号注册
//
// `code` 可填可不填（2026-11-03）：生产环境还没有短信通道，见 `BindMobileReq`
// 的说明。没填时 `mobile_verified = false`，密码本来就是必填，所以「回来的路」
// 是有的 —— 这是它和 `BindMobileReq` 唯一的不同。
type RegisterReq struct {
	Mobile   string `json:"mobile" binding:"required,min=8,max=24"`   // 手机号（含国家码）
	Password string `json:"password" binding:"required,min=6,max=64"` // 密码
	Code     string `json:"code" binding:"omitempty,len=6,numeric"`   // 短信验证码（可选）
	Nickname string `json:"nickname" binding:"omitempty,max=32"`      // 昵称（可选，默认用手机号后 4 位）
}

// GuestLoginReq 游客进入（免注册、免验证码）。
//
// device_id 也可以走 `X-Device-ID` 请求头 —— 其它登录接口一直用这个头，
// 客户端不必为这一个接口换个传法。
type GuestLoginReq struct {
	DeviceID string `json:"device_id" binding:"omitempty,max=64"`
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

// BindMobileReq 游客绑定手机号（转正式账号）
//
// ## 验证码可以不填（2026-11-03）
//
// 生产环境**还没有短信通道**（`DevSmsSender` 在 `IsProduction()` 下直接返回
// 错误），所以「必须填验证码」等于「永远绑不上」。这里改成可选：
//
//   · 填了 ⇒ 照常校验，`mobile_verified = true`；
//   · 没填 ⇒ 允许，但**必须同时设密码**（否则换台手机就永久回不来，
//     见 `errors.BindNeedPassword`），并且 `mobile_verified = false`。
//
// ⚠️ 没填验证码时**不许绑别人的号**：手机号已经被另一个账号占着就直接拒
// （`MobileAlreadyReg`），所以「抢号」最多抢到一个谁都没用过的空号。
type BindMobileReq struct {
	Mobile   string `json:"mobile" binding:"required,min=8,max=24"`
	Code     string `json:"code" binding:"omitempty,len=6,numeric"`
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

// WalletSpendReq 扣积分（插件游戏开局这类「游戏内消费」）。
//
// ## 为什么需要一个通用扣分接口
//
// 在线版的积分以服务端为准，但**插件游戏扣分原本只写本地账本**。
// 那会静默漂移：本地多一条 `lg_` 行、服务端没有，而拉取只清 `srv_` 行 ——
// 那条本地行永远留着，余额就此对不上，界面上什么都看不出来。
//
// ## `Source` 白名单只有一个值
//
// 只允许 `game_spend`。放开通用的 `manual_adjust` 等于给了客户端
// 「任意改写流水来源」的能力，而来源正是家长看流水时的唯一线索。
type WalletSpendReq struct {
	Amount int64 `json:"amount" binding:"required,min=1,max=100000"`
	// Source 来源，缺省 `game_spend`
	Source string `json:"source" binding:"omitempty,max=32"`
	// RefID 关联对象（哪款游戏扣的）
	RefID string `json:"ref_id" binding:"omitempty,max=64"`
	Memo  string `json:"memo" binding:"omitempty,max=128"`
	// ClientRequestID 幂等键
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// WalletImportReq 把「本地版留下的余额」一次性结转进这个账号。
//
// ## 为什么需要它
//
// 本地版和在线版共用同一个包名、同一套表。从本地版升级上来的设备，Dexie 的
// 三张账本里还留着旧行；而在线版的余额是**整表求和**（`currentBalance` 之类，
// 不过滤云端镜像行），于是界面显示「服务端 + 本机旧账」，服务端只认自己那份。
// 实测（2026-11-03）：服务端 `points=219`，界面显示 `1219`，重开还是 1219。
// 孩子看到 1219 分，兑换时却被服务端拒掉，而且只说「积分不够」。
//
// ## 只搬余额，不搬流水
//
// 逐条搬历史等于让客户端和服务端各持一套「同一个事实」的账：任何一次重试都
// 可能多记一笔，而本地流水里的 `refId` 在服务端根本不存在。所以每种币最多写
// **一行**「结转」，备注带上笔数 —— 家长看流水时能一眼认出来。
//
// ## 幂等
//
// 幂等键 = `legacy_import:<uid>:<key>:<currency>`，`key` 由客户端给（安装 id
// + 载荷哈希）。同一个安装重复调用只生效一次；换一台设备、载荷不同 ⇒ 各写各的，
// 不会互相吞掉。
type WalletImportReq struct {
	// Key 幂等键（客户端给）
	Key string `json:"key" binding:"required,min=1,max=64"`
	// Points 要结转的积分
	Points int64 `json:"points" binding:"min=0,max=1000000"`
	// Coupons 要结转的兑换券
	Coupons int64 `json:"coupons" binding:"min=0,max=1000000"`
	// Diamonds 要结转的钻石
	Diamonds int64 `json:"diamonds" binding:"min=0,max=1000000"`
	// Counts 各币种的笔数，只用来写备注（服务端不校验，客户端统计）
	Counts map[string]int64 `json:"counts" binding:"omitempty"`
}

// WalletImportResp 结转结果
type WalletImportResp struct {
	// Imported 这次真的写进去了没有。false = 之前已经结转过（重试）
	Imported bool `json:"imported"`
	// 这次写进去的数量（重复调用时都是 0）
	Points   int64 `json:"points"`
	Coupons  int64 `json:"coupons"`
	Diamonds int64 `json:"diamonds"`
	// Wallet 结转之后的余额
	Wallet *WalletInfo `json:"wallet"`
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
