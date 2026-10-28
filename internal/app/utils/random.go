package utils

import (
	"bytes"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"time"
)

var (
	randSource = mrand.NewSource(time.Now().UnixNano())
	encoding   = base32.NewEncoding("ybndrfg8ejkmcpqxot1uwisza345h769").WithPadding(base32.NoPadding)
)

// NewRandomString 生成指定长度的随机字符串（自定义 base32 字典）
func NewRandomString(length int) string {
	var b bytes.Buffer
	str := make([]byte, length+8)
	_, _ = mrand.New(randSource).Read(str)
	enc := base32.NewEncoder(encoding, &b)
	_, _ = enc.Write(str)
	_ = enc.Close()
	out := b.String()
	if len(out) > length {
		out = out[:length]
	}
	return out
}

// NewNonce 生成 32 位十六进制随机串
func NewNonce() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// GenerateSecureHex 生成 64 位十六进制随机串（32 字节）
func GenerateSecureHex() (string, error) {
	b := make([]byte, 32)
	if _, err := crand.Read(b); err != nil {
		return "", fmt.Errorf("generate secure random failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// GenerateDeviceID 生成 32 位设备标识
func GenerateDeviceID() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// HMACSHA256Hex 计算 HMAC-SHA256 十六进制签名
func HMACSHA256Hex(key, message string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}
