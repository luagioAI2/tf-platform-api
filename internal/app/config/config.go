package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

var config Config

// ============================ 服务 ============================

type ServiceSettings struct {
	NodeID          string `yaml:"NodeID"`
	ListenAddress   string `yaml:"ListenAddress"`
	StaticPath      string `yaml:"StaticPath"`
	Timezone        string `yaml:"Timezone"`
	Environment     string `yaml:"Environment"` // development | test | production
	Language        string `yaml:"Language"`    // 默认语言 en/es/zh
	Area            string `yaml:"Area"`
	CountryCode     string `yaml:"CountryCode"`
	StorageHost     string `yaml:"StorageHost"`
	TrustedUserCode string `yaml:"TrustedUserCode"` // 内部调试验证码

	// JWT（C 端）
	JwtSignKey        string `yaml:"JwtSignKey"`
	TokenExpireHours  int    `yaml:"TokenExpireHours"`  // access token 有效期（小时）
	TokenRefreshHours int    `yaml:"TokenRefreshHours"` // refresh token 有效期（小时）

	// 后台令牌
	AdminSignKey          string `yaml:"AdminSignKey"`
	AdminTokenExpireHours int    `yaml:"AdminTokenExpireHours"`

	// 开关
	EnablePprof   bool `yaml:"EnablePprof"`
	EnableSwagger bool `yaml:"EnableSwagger"`
	EnableMQ      bool `yaml:"EnableMQ"`
	EnableTask    bool `yaml:"EnableTask"`
	EnableTrace   bool `yaml:"EnableTrace"`   // 访问日志中间件
	EnableCaptcha bool `yaml:"EnableCaptcha"` // 图形验证码
	EnableMetrics bool `yaml:"EnableMetrics"` // Prometheus /metrics

	// ===== 性能与保护（5W 并发关键配置）=====

	// MaxBodyBytes 单个请求体上限（字节）。0 表示不限制。
	// 建议 4MB。
	//
	// ⚠️ 这里原来写的是「文件上传接口单独用 MaxMultipartMemory 控制」—— **不对**。
	//    `gin.MaxMultipartMemory` 只是「解析 multipart 时内存里放多少」，
	//    超了会落临时文件，它**不拒绝任何请求**。真正拒绝的是本中间件。
	//    结果是：全局 4MB 把游戏包上传也一起卡死（实测 21 款里 13 款 > 4MB，
	//    `claw3d` 4.98MB 直接 `10030201 文件过大`，而报错里连上限都没说）。
	//    要放宽的是 UploadMaxBodyBytes 那条**按路径的例外**，见下。
	MaxBodyBytes int64 `yaml:"MaxBodyBytes"`

	// UploadTimeoutSeconds 「按路径例外」的超时：游戏包上传。
	// 0 表示跟 RequestTimeoutSeconds 一样。
	//
	// 为什么必须单独给：20 秒是给 JSON 接口的。6MB 的包在 150KB/s 的链路上要 40 秒，
	// 实测 24 秒就被 504 掐断。**局域网 1.4 秒传完，所以本地永远试不出来。**
	UploadTimeoutSeconds int `yaml:"UploadTimeoutSeconds"`

	// UploadMaxBodyBytes 「按路径例外」的上限：游戏包上传（`/api/admin/game/release/upload`）。
	// 0 表示跟 MaxBodyBytes 一样。插件包现在 0.1~6MB，给 64MB 足够，
	// 再大就不该走这条同步上传了（该走 OSS + 填 URL 那条路）。
	UploadMaxBodyBytes int64 `yaml:"UploadMaxBodyBytes"`

	// RequestTimeoutSeconds 单请求处理超时（秒）。0 表示不限制。
	// 超时后返回 504，并通过 context 中断下游 DB/Redis 调用。
	RequestTimeoutSeconds int `yaml:"RequestTimeoutSeconds"`

	// TrustedProxies 可信代理网段。为空时**不信任**任何 X-Forwarded-For，
	// 避免客户端伪造 IP 绕过限流。部署在 Nginx/SLB 后必须配置。
	TrustedProxies []string `yaml:"TrustedProxies"`

	// ReadHeaderTimeoutSeconds 读请求头超时（防慢速攻击）
	ReadHeaderTimeoutSeconds int `yaml:"ReadHeaderTimeoutSeconds"`
	// ReadTimeoutSeconds 读请求体超时
	ReadTimeoutSeconds int `yaml:"ReadTimeoutSeconds"`
	// WriteTimeoutSeconds 写响应超时
	WriteTimeoutSeconds int `yaml:"WriteTimeoutSeconds"`
	// IdleTimeoutSeconds 空闲连接保持时间
	IdleTimeoutSeconds int `yaml:"IdleTimeoutSeconds"`
	// MaxHeaderBytes 请求头最大字节数
	MaxHeaderBytes int `yaml:"MaxHeaderBytes"`

	// ShutdownTimeoutSeconds 优雅关闭超时
	ShutdownTimeoutSeconds int `yaml:"ShutdownTimeoutSeconds"`

	// ===== 限流（全局默认值，单接口可覆盖）=====
	RateLimit RateLimitSettings `yaml:"RateLimit"`
}

// RateLimitSettings 限流配置
type RateLimitSettings struct {
	// Enable 是否开启全局限流
	Enable bool `yaml:"Enable"`
	// GlobalLimit 单 IP 在 GlobalWindow 内允许的请求数
	GlobalLimit int `yaml:"GlobalLimit"`
	// GlobalWindowSeconds 全局窗口（秒）
	GlobalWindowSeconds int `yaml:"GlobalWindowSeconds"`
}

func (s ServiceSettings) IsTest() bool        { return s.Environment == "test" }
func (s ServiceSettings) IsDevelopment() bool { return s.Environment == "development" }
func (s ServiceSettings) IsProduction() bool  { return s.Environment == "production" }

func (s ServiceSettings) GetEnvName() string {
	switch s.Environment {
	case "production":
		return "正式服"
	case "development":
		return "测试服"
	default:
		return "本地"
	}
}

// Validate 启动期配置校验：宁可启动失败，也不要带着错误配置跑
func (s ServiceSettings) Validate() error {
	if s.ListenAddress == "" {
		return errors.New("ServiceSettings.ListenAddress is required")
	}
	if s.JwtSignKey == "" {
		return errors.New("ServiceSettings.JwtSignKey is required")
	}
	if len(s.JwtSignKey) < 16 {
		return errors.New("ServiceSettings.JwtSignKey is too short (min 16 chars)")
	}
	if s.AdminSignKey == "" {
		return errors.New("ServiceSettings.AdminSignKey is required")
	}
	if s.AdminSignKey == s.JwtSignKey {
		return errors.New("ServiceSettings.AdminSignKey must differ from JwtSignKey")
	}
	if s.Timezone != "" {
		if _, err := loadLocation(s.Timezone); err != nil {
			return fmt.Errorf("invalid ServiceSettings.Timezone %q: %w", s.Timezone, err)
		}
	}

	// 生产环境强制校验（这些配置错了会在高并发下直接雪崩）
	if s.IsProduction() {
		if len(s.TrustedProxies) == 0 {
			return errors.New("ServiceSettings.TrustedProxies is required in production (否则 X-Forwarded-For 可被伪造)")
		}
		if s.MaxBodyBytes <= 0 {
			return errors.New("ServiceSettings.MaxBodyBytes must be > 0 in production")
		}
		if s.RequestTimeoutSeconds <= 0 {
			return errors.New("ServiceSettings.RequestTimeoutSeconds must be > 0 in production")
		}
	}
	if s.MaxBodyBytes < 0 {
		return errors.New("ServiceSettings.MaxBodyBytes must be >= 0")
	}
	if s.RequestTimeoutSeconds < 0 {
		return errors.New("ServiceSettings.RequestTimeoutSeconds must be >= 0")
	}
	if s.ReadHeaderTimeoutSeconds < 0 || s.ReadTimeoutSeconds < 0 || s.WriteTimeoutSeconds < 0 {
		return errors.New("ServiceSettings timeouts must be >= 0")
	}
	return nil
}

// HTTPTimeout 返回 HTTP Server 的超时配置（带默认值兜底）
func (s ServiceSettings) HTTPTimeout() (readHeader, read, write, idle time.Duration, maxHeader int) {
	sec := func(v, def int) time.Duration {
		if v <= 0 {
			v = def
		}
		return time.Duration(v) * time.Second
	}
	maxHeader = s.MaxHeaderBytes
	if maxHeader <= 0 {
		maxHeader = 1 << 20 // 1MB
	}
	return sec(s.ReadHeaderTimeoutSeconds, 5),
		sec(s.ReadTimeoutSeconds, 30),
		sec(s.WriteTimeoutSeconds, 60),
		sec(s.IdleTimeoutSeconds, 90),
		maxHeader
}

// ============================ 数据库 ============================

type DBSettings struct {
	Driver                   string   `yaml:"Driver"`
	DataSource               string   `yaml:"DataSource"`
	DataSourceReplicas       []string `yaml:"DataSourceReplicas"`       // 写库（额外）
	DataSourceSearchReplicas []string `yaml:"DataSourceSearchReplicas"` // 读库
	MaxIdleConns             int      `yaml:"MaxIdleConns"`
	MaxOpenConns             int      `yaml:"MaxOpenConns"`
	ConnMaxLifetimeSeconds   int      `yaml:"ConnMaxLifetimeSeconds"`
	QueryTimeout             int      `yaml:"QueryTimeout"`
	EnableAutoMigrate        bool     `yaml:"EnableAutoMigrate"` // 仅本地/测试开启
	Trace                    bool     `yaml:"Trace"`
	LogLevel                 string   `yaml:"LogLevel"` // silent | error | warn | info
}

// ============================ Redis ============================

type RDBSettings struct {
	DataSource   string `yaml:"DataSource"`
	Password     string `yaml:"Password"`
	DB           int    `yaml:"DB"`
	PoolSize     int    `yaml:"PoolSize"`
	MinIdleConns int    `yaml:"MinIdleConns"`
	PoolTimeout  int    `yaml:"PoolTimeout"` // 秒
}

// ============================ CORS ============================

type CorsSettings struct {
	AllowOrigins     []string `yaml:"AllowOrigins"`
	AllowMethods     []string `yaml:"AllowMethods"`
	AllowHeaders     []string `yaml:"AllowHeaders"`
	ExposeHeaders    []string `yaml:"ExposeHeaders"`
	AllowCredentials bool     `yaml:"AllowCredentials"`
	MaxAge           int      `yaml:"MaxAge"` // 秒
}

// ============================ 上传 ============================

type UploadSettings struct {
	Type              string                     `yaml:"Type"` // local | minio | aws-s3 | cloudflare-r2
	MaxFileSize       int64                      `yaml:"MaxFileSize"`
	AllowedExtensions []string                   `yaml:"AllowedExtensions"`
	Local             LocalUploadSettings        `yaml:"Local"`
	Minio             MinioUploadSettings        `yaml:"Minio"`
	AwsS3             AwsS3UploadSettings        `yaml:"AwsS3"`
	CloudflareR2      CloudflareR2UploadSettings `yaml:"CloudflareR2"`
}

type LocalUploadSettings struct {
	StorePath string `yaml:"StorePath"`
	BaseURL   string `yaml:"BaseURL"`
}

type MinioUploadSettings struct {
	Endpoint   string `yaml:"Endpoint"`
	AccessKey  string `yaml:"AccessKey"`
	SecretKey  string `yaml:"SecretKey"`
	BucketName string `yaml:"BucketName"`
	UseSSL     bool   `yaml:"UseSSL"`
	BaseURL    string `yaml:"BaseURL"`
	BasePath   string `yaml:"BasePath"`
}

type AwsS3UploadSettings struct {
	Region           string `yaml:"Region"`
	Endpoint         string `yaml:"Endpoint"`
	Bucket           string `yaml:"Bucket"`
	AccessKey        string `yaml:"AccessKey"`
	SecretKey        string `yaml:"SecretKey"`
	BaseURL          string `yaml:"BaseURL"`
	PathPrefix       string `yaml:"PathPrefix"`
	S3ForcePathStyle bool   `yaml:"S3ForcePathStyle"`
	DisableSSL       bool   `yaml:"DisableSSL"`
}

type CloudflareR2UploadSettings struct {
	AccountID       string `yaml:"AccountID"`
	AccessKeyID     string `yaml:"AccessKeyID"`
	SecretAccessKey string `yaml:"SecretAccessKey"`
	Bucket          string `yaml:"Bucket"`
	BaseURL         string `yaml:"BaseURL"`
	PathPrefix      string `yaml:"PathPrefix"`
}

// ============================ 验证码 ============================

type CaptchaSettings struct {
	KeyLong            int  `yaml:"KeyLong"`
	ImgWidth           int  `yaml:"ImgWidth"`
	ImgHeight          int  `yaml:"ImgHeight"`
	OpenCaptcha        bool `yaml:"OpenCaptcha"`
	OpenCaptchaTimeout int  `yaml:"OpenCaptchaTimeout"`
}

// ============================ 微信 ============================

// WechatSetting 微信小程序/公众号配置。
//
// 未配置时（AppID/AppSecret 为空）：
//   - 开发环境：Code2Session 返回 mock 数据，便于联调
//   - 生产环境：直接报错，避免"看起来能用实际不能"的假象
type WechatSetting struct {
	AppID           string `yaml:"AppID"`
	AppSecret       string `yaml:"AppSecret"`
	Code2SessionURL string `yaml:"Code2SessionURL"` // 可覆盖（代理/测试用）
	// MchID / APIKey 支付相关（本轮未使用，预留）
	MchID  string `yaml:"MchID"`
	APIKey string `yaml:"APIKey"`
}

// ============================ 游戏分发 ============================

// GameDistributionSetting 游戏包分发配置。
//
// ## 为什么必须用 OSS/CDN（而不是让 API 进程发文件）
//
// `kid-quest-farm-2d` 的 `public/games/` 实测 **1492 个文件 / 169 MB**
// （21 款游戏，单包 3~6 MB）。这个体量走 API 进程有两个硬问题：
//
//  1. Go 的 HTTP 服务每连接占一个 goroutine + 缓冲，几十 MB 的传输
//     会把连接数和内存打满，正常 API 请求被挤掉；
//  2. 带宽成本落在应用实例上，扩容只能加实例，比 CDN 贵一个量级。
//
// 所以：**包体放对象存储/CDN，客户端直连下载**，
// 服务端只负责「发目录 + 发签名 + 记录版本」。
type GameDistributionSetting struct {
	// CatalogPrivateKey Ed25519 私钥（base64）。
	//
	// **生产环境必须用环境变量 TF_GAME_CATALOG_KEY 覆盖，绝不提交进仓库。**
	// 空值 = 不能签名 = 目录接口报错（fail closed）。
	CatalogPrivateKey string `yaml:"CatalogPrivateKey"`

	// CatalogBaseURL 客户端拉取目录的基址（CDN/OSS 上的静态目录）。
	//
	// 留空则用本服务的 `/api/game/catalog`（便于本地联调）。
	// 生产建议指向 CDN，把目录也放进缓存。
	CatalogBaseURL string `yaml:"CatalogBaseURL"`

	// PackageBaseURL 包下载根地址（OSS/CDN）。
	//
	// `GameRelease.URL` 存相对路径时，会和这个拼成完整地址。
	// 存了绝对 URL（http/https 开头）则直接用。
	PackageBaseURL string `yaml:"PackageBaseURL"`

	// CatalogCacheSeconds 目录构建结果的内存缓存秒数（0 = 不缓存）
	//
	// 目录要遍历全部 active 版本 + JSON 序列化 + 签名，
	// 每次请求都做是浪费。但发布新版本后必须能及时生效 ——
	// `Publish` 里会主动失效缓存。
	CatalogCacheSeconds int `yaml:"CatalogCacheSeconds"`

	// SignedURLTTLSeconds 下载签名 URL 有效期（秒）。
	//
	// 0 = 用公开 CDN 地址（不签名）。公开地址是否可接受取决于业务：
	// 包体是游戏代码，不是密钥；而且客户端本来就会验 SHA-256，
	// 所以**完整性**靠签名目录保证，公开地址不影响安全。
	// 但如果你想挡住「非用户也能下包」，就设一个非 0 值走签名 URL。
	SignedURLTTLSeconds int `yaml:"SignedURLTTLSeconds"`

	// MaxPackageBytes 单包体积上限（字节）。0 = 用默认 64MB
	MaxPackageBytes int64 `yaml:"MaxPackageBytes"`
}

// ============================ 根配置 ============================

type Config struct {
	ServiceSettings ServiceSettings `json:"ServiceSettings" yaml:"ServiceSettings"`
	DBSettings      DBSettings      `json:"DBSettings" yaml:"DBSettings"`
	RDBSettings     RDBSettings     `json:"RDBSettings" yaml:"RDBSettings"`
	CorsSettings    CorsSettings    `json:"CorsSettings" yaml:"CorsSettings"`
	UploadSettings  UploadSettings  `json:"UploadSettings" yaml:"UploadSettings"`
	CaptchaSettings CaptchaSettings `json:"CaptchaSettings" yaml:"CaptchaSettings"`
	WechatSetting   WechatSetting   `json:"WechatSetting" yaml:"WechatSetting"`
	// GameDistribution 游戏包分发（目录签名 / OSS / CDN）
	GameDistribution GameDistributionSetting `json:"GameDistribution" yaml:"GameDistribution"`
}

// Get 返回全局配置（只读，调用方不要修改）
func Get() Config { return config }

// MustLoad 读取并校验配置文件，失败直接 panic（启动期）
func MustLoad(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		panic(fmt.Errorf("read config file %s failed: %w", filePath, err))
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		panic(fmt.Errorf("unmarshal config file %s failed: %w", filePath, err))
	}
	applyEnvOverrides(&config)
	if err := config.ServiceSettings.Validate(); err != nil {
		panic(fmt.Errorf("invalid config: %w", err))
	}
}

// applyEnvOverrides 允许用环境变量覆盖敏感项，避免把密钥写进仓库。
// 优先级：环境变量 > 配置文件。
func applyEnvOverrides(c *Config) {
	if v := os.Getenv("TF_DB_DSN"); v != "" {
		c.DBSettings.DataSource = v
	}
	if v := os.Getenv("TF_REDIS_ADDR"); v != "" {
		c.RDBSettings.DataSource = v
	}
	if v := os.Getenv("TF_REDIS_PASSWORD"); v != "" {
		c.RDBSettings.Password = v
	}
	if v := os.Getenv("TF_JWT_KEY"); v != "" {
		c.ServiceSettings.JwtSignKey = v
	}
	if v := os.Getenv("TF_ADMIN_KEY"); v != "" {
		c.ServiceSettings.AdminSignKey = v
	}
	if v := os.Getenv("TF_ENV"); v != "" {
		c.ServiceSettings.Environment = v
	}
	if v := os.Getenv("TF_LISTEN"); v != "" {
		c.ServiceSettings.ListenAddress = v
	}
	if v := os.Getenv("TF_CORS_ORIGINS"); v != "" {
		c.CorsSettings.AllowOrigins = strings.Split(v, ",")
	}
	if v := os.Getenv("TF_TRUSTED_PROXIES"); v != "" {
		c.ServiceSettings.TrustedProxies = strings.Split(v, ",")
	}
	if v := os.Getenv("TF_METRICS"); v != "" {
		c.ServiceSettings.EnableMetrics = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("TF_RATE_LIMIT"); v != "" {
		c.ServiceSettings.RateLimit.Enable = v != "0" && !strings.EqualFold(v, "false")
	}
	if v := os.Getenv("TF_RATE_LIMIT_GLOBAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.ServiceSettings.RateLimit.GlobalLimit = n
		}
	}
	if v := os.Getenv("TF_WECHAT_APPID"); v != "" {
		c.WechatSetting.AppID = v
	}
	if v := os.Getenv("TF_WECHAT_SECRET"); v != "" {
		c.WechatSetting.AppSecret = v
	}
	// 游戏分发：私钥只走环境变量（绝不写进配置文件）
	if v := os.Getenv("TF_GAME_CATALOG_KEY"); v != "" {
		c.GameDistribution.CatalogPrivateKey = v
	}
	if v := os.Getenv("TF_GAME_CATALOG_URL"); v != "" {
		c.GameDistribution.CatalogBaseURL = v
	}
	if v := os.Getenv("TF_GAME_PACKAGE_URL"); v != "" {
		c.GameDistribution.PackageBaseURL = v
	}
}

// secretKeyRe 命中就按密钥处理（大小写不敏感，按**字段名**匹配）。
//
// ★★ 为什么改成按名字扫，而不是继续一条条手写（2026-11-07 事故）：
//
//	原来的白名单里漏了 `GameDistribution.CatalogPrivateKey` ——
//	启动日志里**把 Ed25519 私钥原样打了出来**（实测 grep 得到完整 base64）。
//	那把钥匙能签一份「任意代码」的游戏目录，下发到孩子手机上。
//	白名单的毛病是「新加一个密钥字段，没人记得回来补一行」，而漏掉**不报错**。
var secretKeyRe = regexp.MustCompile(`(?i)(secret|password|privatekey|private_key|apikey|api_key|accesskey|access_key|signkey|sign_key|token)`)

// maskValue 把整个配置树里**名字像密钥**的字符串值换成 `ab***yz`。
//
// 保留首尾各 2 字符是为了排查「线上配的是哪把钥匙」——
// 全打 `***` 的话，运维没法区分「没配」和「配错了」。
func maskValue(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, sub := range x {
			if s, ok := sub.(string); ok && secretKeyRe.MatchString(k) {
				x[k] = mask(s)
				continue
			}
			x[k] = maskValue(sub)
		}
		return x
	case []interface{}:
		for i, sub := range x {
			x[i] = maskValue(sub)
		}
		return x
	default:
		return v
	}
}

// PrintWithJSON 输出脱敏后的配置（用于启动日志）
func PrintWithJSON() string {
	safe := config
	/* 数据库连接串里带账号密码，但它整个是**一个字符串**（字段名叫 DataSource，
	   不命中 secretKeyRe），所以单独处理：整条换掉，只留类型。
	   副本 / 搜索库一律不打（生产会配多台，泄漏面更大）。 */
	safe.DBSettings.DataSource = mask(safe.DBSettings.DataSource)
	safe.DBSettings.DataSourceReplicas = nil
	safe.DBSettings.DataSourceSearchReplicas = nil

	b, err := json.Marshal(safe)
	if err != nil {
		return "{}"
	}
	// 先转成 map 再按字段名扫一遍，剩下的密钥字段不用再一个个列
	var tree interface{}
	if err := json.Unmarshal(b, &tree); err != nil {
		return "{}"
	}
	out, err := json.Marshal(maskValue(tree))
	if err != nil {
		return "{}"
	}
	return string(out)
}

// mask 只保留首尾各 2 个字符
func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 6 {
		return "***"
	}
	return s[:2] + "***" + s[len(s)-2:]
}
