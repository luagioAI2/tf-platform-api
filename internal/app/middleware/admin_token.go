package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/errors"
	"tf-api/internal/app/ginx"
	"tf-api/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TokenManager 后台 HMAC 令牌管理器。
//
// 令牌格式（Base64URL 编码）：<timestamp>:<userID>:<hmac-sha256-hex>
// 签名内容：userID:timestamp，密钥为 ServiceSettings.AdminSignKey。
type TokenManager struct {
	appKey string
}

func NewTokenManager(appKey string) *TokenManager {
	return &TokenManager{appKey: appKey}
}

// GenerateToken 生成后台令牌
func (tm *TokenManager) GenerateToken(userID string, timestamp int64) string {
	data := fmt.Sprintf("%s:%d", userID, timestamp)
	h := hmac.New(sha256.New, []byte(tm.appKey))
	h.Write([]byte(data))
	sig := hex.EncodeToString(h.Sum(nil))
	raw := fmt.Sprintf("%d:%s:%s", timestamp, userID, sig)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// ValidateToken 校验后台令牌，返回 userID
func (tm *TokenManager) ValidateToken(token string, maxAge time.Duration) (string, error) {
	if tm.appKey == "" {
		return "", fmt.Errorf("admin sign key is empty")
	}
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("invalid token format")
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token parts")
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid timestamp")
	}
	if time.Since(time.Unix(ts, 0)) > maxAge {
		return "", fmt.Errorf("token expired")
	}

	userID, providedSig := parts[1], parts[2]

	// 用相同输入重算签名，常量时间比较
	h := hmac.New(sha256.New, []byte(tm.appKey))
	h.Write([]byte(fmt.Sprintf("%s:%d", userID, ts)))
	expected := h.Sum(nil)

	provided, err := hex.DecodeString(providedSig)
	if err != nil {
		return "", fmt.Errorf("invalid signature format")
	}
	if !hmac.Equal(provided, expected) {
		return "", fmt.Errorf("invalid signature")
	}
	return userID, nil
}

// AdminMiddleware 后台接口鉴权。
//
// 令牌只从 X-Api-Token 请求头读取（不再支持 ?token=，避免令牌进入访问日志）。
func AdminMiddleware() gin.HandlerFunc {
	cfg := config.Get().ServiceSettings
	tm := NewTokenManager(cfg.AdminSignKey)
	maxAge := time.Duration(cfg.AdminTokenExpireHours) * time.Hour
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}

	return func(c *gin.Context) {
		token := c.GetHeader("X-Api-Token")
		if token == "" {
			logger.ZWarn("admin token missing", zap.String("path", c.Request.URL.Path))
			ginx.ResJSON(c, http.StatusUnauthorized, &ginx.Resp{
				Code: errors.AdminTokenRequired,
				Msg:  "admin token required",
			})
			c.Abort()
			return
		}

		userID, err := tm.ValidateToken(token, maxAge)
		if err != nil {
			logger.ZWarn("admin token invalid",
				zap.String("path", c.Request.URL.Path),
				zap.Error(err))
			ginx.ResJSON(c, http.StatusUnauthorized, &ginx.Resp{
				Code: errors.AdminTokenInvalid,
				Msg:  "invalid admin token",
			})
			c.Abort()
			return
		}

		c.Set("userID", userID)
		c.Set("isAdmin", true)
		c.Next()
	}
}
