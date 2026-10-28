package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	// 建议 4MB；文件上传接口单独用 MaxMultipartMemory 控制。
	MaxBodyBytes int64 `yaml:"MaxBodyBytes"`

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

// ============================ 根配置 ============================

type Config struct {
	ServiceSettings ServiceSettings `json:"ServiceSettings" yaml:"ServiceSettings"`
	DBSettings      DBSettings      `json:"DBSettings" yaml:"DBSettings"`
	RDBSettings     RDBSettings     `json:"RDBSettings" yaml:"RDBSettings"`
	CorsSettings    CorsSettings    `json:"CorsSettings" yaml:"CorsSettings"`
	UploadSettings  UploadSettings  `json:"UploadSettings" yaml:"UploadSettings"`
	CaptchaSettings CaptchaSettings `json:"CaptchaSettings" yaml:"CaptchaSettings"`
	WechatSetting   WechatSetting   `json:"WechatSetting" yaml:"WechatSetting"`
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
}

// PrintWithJSON 输出脱敏后的配置（用于启动日志）
func PrintWithJSON() string {
	safe := config
	safe.DBSettings.DataSource = mask(safe.DBSettings.DataSource)
	safe.DBSettings.DataSourceReplicas = nil
	safe.DBSettings.DataSourceSearchReplicas = nil
	safe.RDBSettings.Password = mask(safe.RDBSettings.Password)
	safe.ServiceSettings.JwtSignKey = mask(safe.ServiceSettings.JwtSignKey)
	safe.ServiceSettings.AdminSignKey = mask(safe.ServiceSettings.AdminSignKey)
	safe.ServiceSettings.TrustedUserCode = mask(safe.ServiceSettings.TrustedUserCode)
	safe.UploadSettings.Minio.SecretKey = mask(safe.UploadSettings.Minio.SecretKey)
	safe.UploadSettings.AwsS3.SecretKey = mask(safe.UploadSettings.AwsS3.SecretKey)
	safe.UploadSettings.CloudflareR2.SecretAccessKey = mask(safe.UploadSettings.CloudflareR2.SecretAccessKey)

	b, err := json.Marshal(safe)
	if err != nil {
		return "{}"
	}
	return string(b)
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
