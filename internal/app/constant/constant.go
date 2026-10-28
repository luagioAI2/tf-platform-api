package constant

import "time"

// ============================ HTTP Header ============================

const (
	HEADER_FORWARDED_PROTO = "X-Forwarded-Proto"
	HEADER_REQUEST_ID      = "X-Request-ID"

	// ACCESS_TOKEN / REFRESH_TOKEN 仅用于响应头回传新令牌，
	// 不再支持通过 Cookie 或 Query 传递（避免令牌进入日志）。
	ACCESS_TOKEN  = "X-Access-Token"
	REFRESH_TOKEN = "X-Refresh-Token"
)

// ============================ Redis Key ============================

const (
	// REDIS_KEY_USER_ACCESS_TOKEN 用户 access token（用于单点登录/踢下线）
	REDIS_KEY_USER_ACCESS_TOKEN = "USER_ACCESS_TOKEN_%d"
	// REDIS_KEY_TOKEN_BLACKLIST 登出后的 token 黑名单（jti）
	REDIS_KEY_TOKEN_BLACKLIST = "TOKEN_BLACKLIST_%s"
	// REDIS_KEY_RATE_LIMIT 通用限流前缀
	REDIS_KEY_RATE_LIMIT = "RATE_LIMIT_%s"
)

// ============================ 缓存过期时间 ============================

const (
	CONFIG_CACHE_EXPIRE_TIME    = 6 * time.Hour       // 配置类缓存
	CONFIG_CACHE_EXPIRE_MONTHLY = 30 * 24 * time.Hour // 月度配置缓存
)

// ============================ 分页 ============================

const (
	DEFAULT_PAGE      = 1
	DEFAULT_PAGE_SIZE = 30
	MAX_PAGE_SIZE     = 200
)

// ============================ 通用状态 ============================

const (
	STATUS_DISABLED uint8 = 0
	STATUS_ENABLED  uint8 = 1
	STATUS_DELETED  uint8 = 2
)
