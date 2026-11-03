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
	// GuestNeedBind 游客账号要花兑换券 / 兑实物，得先绑定手机号。
	//
	// 不复用 MobileNotBind：那个码的含义是「微信用户还没绑手机」，
	// 前端见到它只会提示去绑；这里的语义是「这一步得有个能找回的账号」，
	// 前端要弹的是「保存进度」，措辞和落点都不一样。
	GuestNeedBind = 10010802
	// BindNeedPassword 不带验证码绑手机号时，必须设一个密码。
	//
	// 为什么必须有：验证码是「证明这个号是你的」，密码是「以后怎么回来」。
	// 现在生产环境还没有短信通道（`DevSmsSender` 会直接报错），所以允许
	// 不带验证码绑定 —— 那就**只剩密码**这一条回来的路。两个都不给，
	// 这个账号换台手机就永久丢了，而且丢的时候不会有任何提示。
	BindNeedPassword = 10010803
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
	// ImportPayloadInvalid 结转内容不合法（全是 0 / 负数 / 超过上限）。
	// 不复用 InvalidParam：调用方看到「参数错误」会去查字段名，
	// 而这里真正的原因是「这笔结转没有意义」，两件事。
	ImportPayloadInvalid = 10020901 // 结转内容不合法
)

// ============================ 游戏域 10 04 xx xx ============================
const (
	GameNotExist          = 10040101 // 游戏不存在
	GameOffline           = 10040201 // 游戏已下架
	GameAlreadyOwned      = 10040301 // 已拥有该游戏
	GameNotOwned          = 10040401 // 未拥有该游戏
	GameNotPurchasable    = 10040501 // 该游戏不可购买
	GameKvTooLarge        = 10040601 // 存档过大
	GameKvVersionConflict = 10040701 // 存档版本冲突（多设备）
	GameCoinInsufficient  = 10040901 // 游戏币不足
	SnapshotNotExist      = 10041001 // 存档快照不存在
	GameCoinRedeemClosed  = 10041201 // 这款游戏没有开放「币换券」
	SettingsInvalid       = 10041301 // 设置内容不合法（不是 JSON 对象 / 空）
	SettingsTooLarge      = 10041302 // 设置内容超过大小上限（带 %d，必须单独一个码）
	// GameKvKeyLimitExceeded 单游戏 key 数量超上限。
	// 原来这里返的是 InvalidParam「参数错误」—— 参数没错，是配额满了，
	// 插件作者照着「参数错误」查半天查不到原因。
	GameKvKeyLimitExceeded = 10041401 // 单游戏 key 数量超出上限
)

// ============================ 农场域 10 11 xx xx ============================
const (
	FarmStateTooLarge  = 10110101 // 农场状态过大
	FarmStateConflict  = 10110201 // 农场状态版本冲突（多设备）
	FarmLedgerBadClaim = 10110301 // 随农场提交的账本条目不合法
)

// ============================ 任务域 10 05 xx xx ============================
const (
	TaskNotExist         = 10050101 // 任务不存在
	TaskInstanceNotExist = 10050201 // 任务实例不存在
	TaskStatusNotAllowed = 10050301 // 任务状态不允许该操作
	TaskAlreadyCheckedIn = 10050401 // 今日已签到
	TaskNotCheckInTask   = 10050501 // 该任务不是签到任务
	TaskTimerNotStarted  = 10050601 // 计时尚未开始
	TaskReviewPending    = 10050701 // 该任务正在等待审核
)

// ============================ 兑换域 10 06 xx xx ============================
const (
	RedeemItemNotExist   = 10060101 // 兑换商品不存在
	RedeemItemOffline    = 10060201 // 兑换商品已下架
	RedeemOnceOnly       = 10060301 // 该商品只能兑换一次
	RedeemLimitExceeded  = 10060401 // 超出每日兑换上限
	RedeemOutOfStock     = 10060501 // 库存不足
	RedeemRecordNotExist = 10060601 // 兑换记录不存在
	RedeemAlreadyHandled = 10060701 // 该兑换已处理（已兑现或已取消）
)

// ============================ 心愿域 10 07 xx xx ============================
const (
	WishItemNotExist     = 10070101 // 心愿商品不存在
	WishItemNotOpen      = 10070201 // 该商品还未开放
	WishItemOffline      = 10070301 // 心愿商品已下架
	WishOrderNotExist    = 10070401 // 心愿单不存在
	WishStatusNotAllowed = 10070501 // 心愿单状态不允许该操作
	WishVoucherShort     = 10070601 // 兑换券不足
	WishBlindBoxEmpty    = 10070701 // 盲盒池已空（该游戏全都拥有了）
	WishGameRequired     = 10070801 // 游戏盒子必须指定游戏
	WishCodeInvalid      = 10070901 // 兑换码无效
	WishCodeUsed         = 10071001 // 兑换码已被使用
	WishCodeExpired      = 10071101 // 兑换码已过期
	WishShipInfoRequired = 10071201 // 需要填写收货信息
)

// ============================ 游戏分发域 10 08 xx xx ============================
const (
	GameReleaseNotExist  = 10080101 // 游戏版本不存在
	GameReleaseDuplicate = 10080201 // 该版本已发布
	GamePackageTooLarge  = 10080301 // 游戏包过大
	GameSHA256Invalid    = 10080401 // SHA-256 格式不合法
	GameCatalogSignFail  = 10080501 // 目录签名失败
)

// ============================ 招商域 10 10 xx xx ============================
//
// ⚠️ 后台/运维域已占用 1009xx，所以招商用 1010xx。
const (
	MerchantApplicationNotExist = 10100101 // 招商申请不存在
	MerchantAlreadyReviewed     = 10100201 // 该申请已审核过
	MerchantAdminURLRequired    = 10100301 // 通过时必须给后台地址
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
	GuestNeedBind:        "guest-need-bind",
	BindNeedPassword:     "bind-need-password",
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
	ImportPayloadInvalid:  "import-payload-invalid",

	GameNotExist:           "game-not-exist",
	GameOffline:            "game-offline",
	GameAlreadyOwned:       "game-already-owned",
	GameNotOwned:           "game-not-owned",
	GameNotPurchasable:     "game-not-purchasable",
	GameKvTooLarge:         "game-kv-too-large",
	GameKvVersionConflict:  "game-kv-version-conflict",
	GameCoinInsufficient:   "game-coin-insufficient",
	SnapshotNotExist:       "snapshot-not-exist",
	GameCoinRedeemClosed:   "game-coin-redeem-closed",
	SettingsInvalid:        "settings-invalid",
	SettingsTooLarge:       "settings-too-large",
	GameKvKeyLimitExceeded: "game-kv-key-limit-exceeded",

	FarmStateTooLarge:  "farm-state-too-large",
	FarmStateConflict:  "farm-state-conflict",
	FarmLedgerBadClaim: "farm-ledger-bad-claim",

	TaskNotExist:         "task-not-exist",
	TaskInstanceNotExist: "task-instance-not-exist",
	TaskStatusNotAllowed: "task-status-not-allowed",
	TaskAlreadyCheckedIn: "task-already-checked-in",
	TaskNotCheckInTask:   "task-not-check-in-task",
	TaskTimerNotStarted:  "task-timer-not-started",
	TaskReviewPending:    "task-review-pending",

	RedeemItemNotExist:   "redeem-item-not-exist",
	RedeemItemOffline:    "redeem-item-offline",
	RedeemOnceOnly:       "redeem-once-only",
	RedeemLimitExceeded:  "redeem-limit-exceeded",
	RedeemOutOfStock:     "redeem-out-of-stock",
	RedeemRecordNotExist: "redeem-record-not-exist",
	RedeemAlreadyHandled: "redeem-already-handled",

	WishItemNotExist:     "wish-item-not-exist",
	WishItemNotOpen:      "wish-item-not-open",
	WishItemOffline:      "wish-item-offline",
	WishOrderNotExist:    "wish-order-not-exist",
	WishStatusNotAllowed: "wish-status-not-allowed",
	WishVoucherShort:     "wish-voucher-short",
	WishBlindBoxEmpty:    "wish-blind-box-empty",
	WishGameRequired:     "wish-game-required",
	WishCodeInvalid:      "wish-code-invalid",
	WishCodeUsed:         "wish-code-used",
	WishCodeExpired:      "wish-code-expired",
	WishShipInfoRequired: "wish-ship-info-required",

	GameReleaseNotExist:  "game-release-not-exist",
	GameReleaseDuplicate: "game-release-duplicate",
	GamePackageTooLarge:  "game-package-too-large",
	GameSHA256Invalid:    "game-sha256-invalid",
	GameCatalogSignFail:  "game-catalog-sign-fail",

	MerchantApplicationNotExist: "merchant-application-not-exist",
	MerchantAlreadyReviewed:     "merchant-already-reviewed",
	MerchantAdminURLRequired:    "merchant-admin-url-required",

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
