package utils

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"tf-api/pkg/logger"

	"github.com/davecgh/go-spew/spew"
)

// PrintPanicStack 捕获 panic 并打印完整堆栈（配合 defer 使用）
func PrintPanicStack(extras ...interface{}) {
	if x := recover(); x != nil {
		logger.Error("Recovered from panic:", x)
		logger.Errorf("PANIC STACKTRACE:\n%s\n", debug.Stack())
		for k := range extras {
			logger.Errorf("EXTRAS#%v DATA:%v\n", k, spew.Sdump(extras[k]))
		}
	}
}

// GetSelfFuncName 返回调用方的函数名
func GetSelfFuncName() string {
	pc, _, _, _ := runtime.Caller(1)
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return ""
	}
	name := fn.Name()
	if i := strings.LastIndex(name, "."); i != -1 {
		return name[i+1:]
	}
	return ""
}

// GetMillis 当前毫秒时间戳
func GetMillis() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// EncodeMD5 计算 MD5 十六进制串
func EncodeMD5(value string) string {
	m := md5.New()
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// HmacSHA256 计算 HMAC-SHA256 十六进制签名
func HmacSHA256(key, message string) string {
	return HMACSHA256Hex(key, message)
}

// MatchDomain 从 URL 中提取域名
func MatchDomain(domain string) (string, error) {
	re := regexp.MustCompile(`(?:https?://)?([^/]+)`)
	matches := re.FindStringSubmatch(domain)
	if len(matches) > 1 {
		return matches[1], nil
	}
	return "", errors.New("domain format is error")
}

// IsValidEmail 邮箱格式校验
func IsValidEmail(input string) bool {
	emailRegex := `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	matched, _ := regexp.MatchString(emailRegex, input)
	return matched
}

// IsValidUsername 用户名只允许字母、数字、下划线
func IsValidUsername(username string) bool {
	return regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(username)
}

// ValidatePassword 密码强度：6-32 位，必须同时包含字母和数字
func ValidatePassword(password string) error {
	if n := len(password); n < 6 || n > 32 {
		return fmt.Errorf("password length must be 6-32 (got %d)", n)
	}
	var hasLetter, hasDigit bool
	for _, c := range password {
		switch {
		case unicode.IsLetter(c):
			hasLetter = true
		case unicode.IsDigit(c):
			hasDigit = true
		}
		if hasLetter && hasDigit {
			break
		}
	}
	if !hasLetter {
		return errors.New("password must contain at least one letter")
	}
	if !hasDigit {
		return errors.New("password must contain at least one digit")
	}
	return nil
}

// GenerateTaskID 生成任务 ID
func GenerateTaskID() string {
	return fmt.Sprintf("task_%d_%s", time.Now().UnixNano(), NewRandomString(8))
}
