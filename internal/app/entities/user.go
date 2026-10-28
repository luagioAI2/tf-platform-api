package entities

// ============================ 用户 ============================

// User 用户账号。
//
// 设计要点：
//   - 支持三种登录方式：手机号+密码、微信 OAuth、游客（可选）
//   - 手机号唯一；微信 openid/unionid 唯一
//   - 密码用 bcrypt，禁止明文
type User struct {
	BaseModel

	// Username 用户名（可选，游客/微信用户可能没有）
	Username string `gorm:"column:username;size:32;index" json:"username"`
	// Password bcrypt 哈希，微信/游客用户可为空
	Password string `gorm:"column:password;size:72" json:"-"`

	// Mobile 手机号（带国家码，如 +8613800138000）
	Mobile string `gorm:"column:mobile;size:24;uniqueIndex" json:"mobile"`
	// MobileVerified 手机号是否已验证
	MobileVerified bool `gorm:"column:mobile_verified;default:false" json:"mobile_verified"`

	// Nickname 昵称
	Nickname string `gorm:"column:nickname;size:32" json:"nickname"`
	// Avatar 头像 URL
	Avatar string `gorm:"column:avatar;size:255" json:"avatar"`
	// Gender 性别：0 未知 1 男 2 女
	Gender uint8 `gorm:"column:gender;default:0" json:"gender"`

	// Status 账号状态：0 禁用 1 正常
	Status uint8 `gorm:"column:status;default:1;index" json:"status"`

	// RegisterIP / LastLoginIP 用于风控
	RegisterIP  string `gorm:"column:register_ip;size:45" json:"-"`
	LastLoginIP string `gorm:"column:last_login_ip;size:45" json:"-"`
	LastLoginAt int64  `gorm:"column:last_login_at;default:0" json:"last_login_at"`

	// Language 语言偏好：zh / en / es
	Language string `gorm:"column:language;size:8;default:'zh'" json:"language"`

	// IsGuest 是否游客账号
	IsGuest bool `gorm:"column:is_guest;default:false;index" json:"is_guest"`
}

func (User) TableName() string { return "user" }

// IsActive 账号是否可用
func (u *User) IsActive() bool { return u.Status == UserStatusActive }

// 账号状态
const (
	UserStatusDisabled uint8 = 0
	UserStatusActive   uint8 = 1
	UserStatusBanned   uint8 = 2
)

// ============================ 第三方账号 ============================

// UserOAuth 第三方账号绑定。
//
// 一个用户可绑定多个第三方账号（微信 + 手机号），
// 一个第三方账号只能绑定一个用户。
type UserOAuth struct {
	BaseModel

	UID uint `gorm:"column:uid;index;not null" json:"uid"`

	// Provider 平台：wechat / apple / google
	Provider string `gorm:"column:provider;size:16;not null" json:"provider"`
	// OpenID 平台内唯一标识（微信 openid）
	OpenID string `gorm:"column:openid;size:64;not null" json:"openid"`
	// UnionID 跨应用唯一标识（微信 unionid，可为空）
	UnionID string `gorm:"column:unionid;size:64;index" json:"unionid"`

	// Nickname / Avatar 第三方返回的资料快照
	Nickname string `gorm:"column:nickname;size:64" json:"nickname"`
	Avatar   string `gorm:"column:avatar;size:512" json:"avatar"`

	// Raw 第三方返回的原始资料（JSON），便于排查
	Raw string `gorm:"column:raw;type:text" json:"-"`
}

func (UserOAuth) TableName() string { return "user_oauth" }

// 第三方平台常量
const (
	OAuthProviderWechat = "wechat"
	OAuthProviderApple  = "apple"
	OAuthProviderGoogle = "google"
)

// ============================ 登录令牌 ============================

// UserToken 登录令牌（refresh token 持久化，access token 走 JWT 无状态）。
//
// 为什么 refresh token 要落库：
//   - 支持"踢下线"（删除该用户所有 token）
//   - 支持"查看登录设备"（未来功能）
type UserToken struct {
	BaseModel

	UID uint `gorm:"column:uid;index;not null" json:"uid"`

	// RefreshToken 刷新令牌（随机串，非 JWT）
	RefreshToken string `gorm:"column:refresh_token;size:64;uniqueIndex;not null" json:"-"`
	// ExpireAt 过期时间（Unix 秒）
	ExpireAt int64 `gorm:"column:expire_at;index;not null" json:"expire_at"`

	// Device / UserAgent / IP 登录环境
	Device    string `gorm:"column:device;size:32" json:"device"`
	UserAgent string `gorm:"column:user_agent;size:255" json:"-"`
	IP        string `gorm:"column:ip;size:45" json:"ip"`

	// Revoked 是否已撤销（登出/踢下线）
	Revoked bool `gorm:"column:revoked;default:false;index" json:"revoked"`
}

func (UserToken) TableName() string { return "user_token" }

// ============================ 验证码 ============================

// SmsCode 短信验证码。
//
// 安全要点：
//   - 只存哈希，不存明文（防止库被拖走后直接登录）
//   - 有尝试次数上限（防爆破）
//   - 有发送频率限制（在 Service 层用 Redis 实现）
type SmsCode struct {
	BaseModel

	// Mobile 手机号
	Mobile string `gorm:"column:mobile;size:24;index;not null" json:"mobile"`
	// CodeHash 验证码哈希（sha256(mobile+code+salt)）
	CodeHash string `gorm:"column:code_hash;size:64;not null" json:"-"`
	// Scene 使用场景：register / login / reset_password / bind_mobile
	Scene string `gorm:"column:scene;size:24;index;not null" json:"scene"`
	// ExpireAt 过期时间（Unix 秒）
	ExpireAt int64 `gorm:"column:expire_at;index;not null" json:"expire_at"`
	// Used 是否已使用
	Used bool `gorm:"column:used;default:false" json:"used"`
	// Attempts 已验证尝试次数（防爆破）
	Attempts int `gorm:"column:attempts;default:0" json:"attempts"`
	// IP 发送请求的 IP
	IP string `gorm:"column:ip;size:45" json:"-"`
}

func (SmsCode) TableName() string { return "sms_code" }

// 验证码场景
const (
	SmsSceneRegister      = "register"
	SmsSceneLogin         = "login"
	SmsSceneResetPassword = "reset_password"
	SmsSceneBindMobile    = "bind_mobile"
)

// 验证码策略
const (
	SmsCodeLength      = 6
	SmsCodeTTLSeconds  = 300 // 5 分钟
	SmsCodeMaxAttempts = 5
)
