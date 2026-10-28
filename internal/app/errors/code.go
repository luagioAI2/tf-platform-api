package errors

// 错误码分段规则：10 + 域(2位) + 序号(4位)
//
//	200         成功
//	5xx         系统级
//	6xx         通用（参数/权限/资源）
//	10 01xx xx  用户域
//	10 02xx xx  订单域（示例，按需扩展）
//	10 03xx xx  文件域
//	10 09xx xx  后台/运维域
const (
	SUCCESS     = 200 // 成功
	ERROR       = 500 // 通用错误
	ServerError = 501 // 服务器错误

	ServiceNotFound      = 601 // 服务不存在
	ResourceNotExist     = 602 // 资源不存在
	VerifyForbidden      = 603 // 验证失败
	PermissionsForbidden = 604 // 权限不足
	UnLogin              = 605 // 未登录
	InvalidParam         = 606 // 参数错误
	ValidationParamError = 607 // 参数校验失败（ginx 自动使用）
	TooManyRequests      = 608 // 请求过于频繁
	OperationFailed      = 609 // 操作失败
	DuplicateOperation   = 610 // 重复操作（幂等拦截）
	StatusNotAllowed     = 611 // 当前状态不允许该操作
	RequestTimeout       = 612 // 请求超时
	ServiceUnavailable   = 613 // 服务暂时不可用（依赖故障/熔断）
)

// ============================ 用户域 10 01 xx xx ============================
const (
	UserNotExist         = 10010101 // 用户不存在
	UserAlreadyExist     = 10010201 // 用户已存在
	MobileAlreadyReg     = 10010202 // 手机号已注册
	InvalidPassword      = 10010301 // 密码错误
	PasswordNotMatch     = 10010302 // 两次密码不一致
	PasswordNotSet       = 10010303 // 尚未设置密码
	AccountDisabled      = 10010401 // 账号已禁用
	TokenInvalid         = 10010501 // 令牌无效
	TokenExpired         = 10010502 // 令牌已过期
	VerifyCodeInvalid    = 10010601 // 验证码错误
	VerifyCodeExpired    = 10010602 // 验证码已过期
	VerifyCodeTooMany    = 10010603 // 验证码尝试次数过多
	SmsSendTooOften      = 10010701 // 短信发送过于频繁
	SmsSendFailed        = 10010702 // 短信发送失败
	MobileNotBind        = 10010801 // 未绑定手机号
	WechatAuthFailed     = 10010901 // 微信授权失败
	WechatAlreadyBind    = 10010902 // 微信已绑定其他账号
	AccountNotBindWechat = 10010903 // 账号未绑定微信
)

// ============================ 钱包域 10 02 xx xx ============================
const (
	OrderNotExist         = 10020101 // 订单不存在
	OrderStatusNotAllowed = 10020201 // 订单状态不允许该操作
	OrderAlreadyClosed    = 10020202 // 订单已关闭
	InsufficientBalance   = 10020301 // 余额不足
	AmountNotAllowed      = 10020401 // 金额不合法
	WalletNotExist        = 10020501 // 钱包不存在
	CurrencyNotSupported  = 10020601 // 币种不支持
	BalanceConflict       = 10020701 // 余额并发冲突，请重试
	DuplicateLedgerEntry  = 10020801 // 重复记账（幂等命中）
)

// ============================ 游戏域 10 04 xx xx ============================
const (
	GameNotExist       = 10040101 // 游戏不存在
	GameOffline        = 10040201 // 游戏已下架
	GameAlreadyOwned   = 10040301 // 已拥有该游戏
	GameNotOwned       = 10040401 // 未拥有该游戏
	GameNotPurchasable = 10040501 // 该游戏不可购买
)

// ============================ 文件域 10 03 xx xx ============================
const (
	FileNotAllowed     = 10030101 // 文件类型不允许
	FileTooLarge       = 10030201 // 文件过大
	FileUploadFailed   = 10030301 // 上传失败
	FileStorageNotInit = 10030401 // 存储未初始化
)

// ============================ 后台/运维域 10 09 xx xx ============================
const (
	AdminTokenRequired = 10090101 // 缺少后台令牌
	AdminTokenInvalid  = 10090102 // 后台令牌无效
	AdminTokenExpired  = 10090103 // 后台令牌已过期
)

// ErrCodeMsg 错误码 → i18n key。
// 注意：value 必须是 configs/i18n/*.yaml 中存在的 key，否则前端会收到 key 原文。
var ErrCodeMsg = map[int]string{
	SUCCESS:     "success",
	ERROR:       "basic-error",
	ServerError: "server-error",

	ServiceNotFound:      "service-not-found",
	ResourceNotExist:     "resource-not-exist",
	VerifyForbidden:      "verification-failed",
	PermissionsForbidden: "insufficient-permissions",
	UnLogin:              "user-not-logged-in",
	InvalidParam:         "invalid-parameters",
	ValidationParamError: "invalid-parameters",
	TooManyRequests:      "too-many-requests",
	OperationFailed:      "operation-failed",
	DuplicateOperation:   "duplicate-operation",
	StatusNotAllowed:     "status-not-allowed",
	RequestTimeout:       "request-timeout",
	ServiceUnavailable:   "service-unavailable",

	UserNotExist:         "user-not-exist",
	UserAlreadyExist:     "user-already-exist",
	MobileAlreadyReg:     "mobile-already-registered",
	InvalidPassword:      "invalid-password",
	PasswordNotMatch:     "password-not-match",
	PasswordNotSet:       "password-not-set",
	AccountDisabled:      "account-disabled",
	TokenInvalid:         "token-invalid",
	TokenExpired:         "token-expired",
	VerifyCodeInvalid:    "verify-code-invalid",
	VerifyCodeExpired:    "verify-code-expired",
	VerifyCodeTooMany:    "verify-code-too-many-attempts",
	SmsSendTooOften:      "sms-send-too-often",
	SmsSendFailed:        "sms-send-failed",
	MobileNotBind:        "mobile-not-bound",
	WechatAuthFailed:     "wechat-auth-failed",
	WechatAlreadyBind:    "wechat-already-bound",
	AccountNotBindWechat: "account-not-bind-wechat",

	OrderNotExist:         "order-not-exist",
	OrderStatusNotAllowed: "order-status-not-allowed",
	OrderAlreadyClosed:    "order-already-closed",
	InsufficientBalance:   "insufficient-balance",
	AmountNotAllowed:      "amount-not-allowed",
	WalletNotExist:        "wallet-not-exist",
	CurrencyNotSupported:  "currency-not-supported",
	BalanceConflict:       "balance-conflict",
	DuplicateLedgerEntry:  "duplicate-ledger-entry",

	GameNotExist:       "game-not-exist",
	GameOffline:        "game-offline",
	GameAlreadyOwned:   "game-already-owned",
	GameNotOwned:       "game-not-owned",
	GameNotPurchasable: "game-not-purchasable",

	FileNotAllowed:     "file-not-allowed",
	FileTooLarge:       "file-too-large",
	FileUploadFailed:   "file-upload-failed",
	FileStorageNotInit: "file-storage-not-init",

	AdminTokenRequired: "admin-token-required",
	AdminTokenInvalid:  "admin-token-invalid",
	AdminTokenExpired:  "admin-token-expired",
}

// GetErrorMsg 返回错误码对应的 i18n key，未注册时返回 basic-error
func GetErrorMsg(ec int) string {
	if key, ok := ErrCodeMsg[ec]; ok {
		return key
	}
	return "basic-error"
}
