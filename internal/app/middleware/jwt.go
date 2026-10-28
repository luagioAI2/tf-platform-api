package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/constant"
	"tf-api/internal/app/errors"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/utils"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// JWTMiddleware 强制登录。令牌只从 Authorization: Bearer <token> 读取。
// 校验顺序：签名 → 过期 → Redis 黑名单（登出后失效）。
func JWTMiddleware(rds *redis.Client) gin.HandlerFunc {
	cfg := config.Get().ServiceSettings

	return func(c *gin.Context) {
		token := extractBearer(c)
		if token == "" {
			abortUnauthorized(c, errors.TokenInvalid)
			return
		}

		claims, err := utils.ParseJWT(token, cfg.JwtSignKey)
		if err != nil {
			abortUnauthorized(c, errors.TokenInvalid)
			return
		}

		if isRevoked(c.Request.Context(), rds, claims.JTI) {
			abortUnauthorized(c, errors.TokenExpired)
			return
		}

		c.Set("userID", claims.UserID)
		c.Set("tokenJTI", claims.JTI)
		c.Next()
	}
}

// OptionalJWTMiddleware 解析令牌但不拦截：解析成功则写入 userID，失败按匿名处理。
func OptionalJWTMiddleware(rds *redis.Client) gin.HandlerFunc {
	cfg := config.Get().ServiceSettings

	return func(c *gin.Context) {
		token := extractBearer(c)
		if token == "" {
			c.Next()
			return
		}
		claims, err := utils.ParseJWT(token, cfg.JwtSignKey)
		if err != nil {
			c.Next()
			return
		}
		if isRevoked(c.Request.Context(), rds, claims.JTI) {
			c.Next()
			return
		}
		c.Set("userID", claims.UserID)
		c.Set("tokenJTI", claims.JTI)
		c.Next()
	}
}

// RevokeToken 把令牌加入黑名单（登出时调用），TTL 应等于令牌剩余有效期。
func RevokeToken(ctx context.Context, rds *redis.Client, jti string, ttl time.Duration) error {
	if rds == nil || jti == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return rds.Set(ctx, blacklistKey(jti), 1, ttl).Err()
}

func isRevoked(ctx context.Context, rds *redis.Client, jti string) bool {
	if rds == nil || jti == "" {
		return false
	}
	n, err := rds.Exists(ctx, blacklistKey(jti)).Result()
	return err == nil && n > 0
}

func blacklistKey(jti string) string {
	return strings.Replace(constant.REDIS_KEY_TOKEN_BLACKLIST, "%s", jti, 1)
}

// extractBearer 从 Authorization 头提取 Bearer token（不支持 query 传参，避免令牌进日志）
func extractBearer(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func abortUnauthorized(c *gin.Context, code int) {
	ginx.ResJSON(c, http.StatusUnauthorized, &ginx.Resp{
		Code: code,
		Msg:  "unauthorized",
	})
	c.Abort()
}
