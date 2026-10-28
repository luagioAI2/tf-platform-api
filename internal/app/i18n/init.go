package i18n

import (
	"path/filepath"
	"strings"

	"tf-api/internal/app/config"

	"github.com/gin-gonic/gin"
)

// supportedLangs 已支持的语言（与 configs/i18n 下的 yaml 文件名一一对应）
var supportedLangs = []string{"en", "es", "zh"}

// InitI18n 初始化多语言系统
func InitI18n(defaultLang string) error {
	dir := filepath.Join("configs", "i18n")
	if defaultLang == "" {
		defaultLang = "en"
	}
	return Init(dir, defaultLang)
}

// GetPreferredLanguage 解析 Accept-Language，返回受支持的语言码
func GetPreferredLanguage(c *gin.Context) string {
	fallback := GetBaseLang(config.Get().ServiceSettings.Language)
	if fallback == "" {
		fallback = "en"
	}

	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		return fallback
	}

	// 取第一个语言项并裁掉 q 值
	first := strings.TrimSpace(strings.SplitN(acceptLang, ",", 2)[0])
	if idx := strings.Index(first, ";"); idx != -1 {
		first = strings.TrimSpace(first[:idx])
	}

	lang := GetBaseLang(first)
	for _, s := range supportedLangs {
		if lang == s {
			return lang
		}
	}
	return fallback
}

// StandardizeLang 把语言码标准化为 xx-YY 形式
func StandardizeLang(lang string) string {
	lang = strings.ToLower(lang)
	switch {
	case strings.HasPrefix(lang, "zh"):
		return "zh-CN"
	case strings.HasPrefix(lang, "es"):
		if strings.Contains(lang, "mx") {
			return "es-MX"
		}
		return "es-ES"
	case strings.HasPrefix(lang, "en"):
		return "en-US"
	default:
		parts := strings.SplitN(lang, "-", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return strings.ToUpper(parts[0][:1]) + parts[0][1:] + "-" + strings.ToUpper(parts[1])
		}
		return lang
	}
}

// GetBaseLang 提取基础语言码（zh-CN → zh）
func GetBaseLang(lang string) string {
	if lang == "" {
		return "en"
	}
	return strings.SplitN(lang, "-", 2)[0]
}
