package utils

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// Claims JWT 载荷。
// JTI 用于登出黑名单；Expire 由 jwt.RegisteredClaims 提供。
type Claims struct {
	UserID string `json:"userID"`
	JTI    string `json:"jti"`
	jwt.RegisteredClaims
}

// GenerateJWT 生成 access/refresh token。
// secret 为空时直接报错，避免用空密钥签名（原始项目未校验）。
func GenerateJWT(secret string, expire time.Duration, userID, jti string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("jwt sign key is empty")
	}
	now := time.Now()
	claims := Claims{
		UserID: userID,
		JTI:    jti,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expire)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseJWT 解析并校验签名与过期时间
func ParseJWT(tokenString, secret string) (*Claims, error) {
	if secret == "" {
		return nil, fmt.Errorf("jwt sign key is empty")
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if claims.UserID == "" {
		return nil, fmt.Errorf("invalid token: empty userID")
	}
	return claims, nil
}
