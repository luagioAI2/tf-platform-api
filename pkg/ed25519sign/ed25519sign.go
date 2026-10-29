// Package ed25519sign 提供游戏目录（catalog.json）的 Ed25519 签名与验签。
//
// # 为什么单独一个包
//
// 签名这件事只允许有一份实现。客户端的验签逻辑在
// `kid-quest-farm-2d/src/games/verify.ts`，服务端如果各写一份，
// 早晚会出现「服务端按 A 格式签、客户端按 B 格式验」，
// 而那种错**不会报错**，只会让所有游戏都装不上。
//
// # 契约（必须与前端一致，改任何一条都要同步改前端）
//
//   - 算法：Ed25519
//   - 公钥：**原始 32 字节**（base64 存在前端 `catalogKey.ts`）
//   - 私钥：64 字节（seed 32 + public 32），base64 存配置
//   - 签名对象：**catalog.json 的原始字节**（Ed25519 内部自己做哈希，
//     不要再套一层 SHA-256）
//   - 签名格式：**原始 64 字节**，`.sig` 文件里是**单行 base64**
//     （前端 `decodeSignature` 用 `atob` 解，所以必须无换行）
//
// # fail closed
//
// 没配私钥 = 不能签 = 目录接口报错。
// **不提供「没配就跳过签名」的分支** —— 那等于让
// 「谁拿下静态服务器，谁就能在孩子手机里跑任意代码」变成默认行为。
// 前端 `catalogKey.ts` 的空公钥也是同一个态度。
package ed25519sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// PublicKeyBytes Ed25519 原始公钥固定 32 字节。
//
// 与前端 `ED25519_PUBLIC_KEY_BYTES` 同一个数字。
const PublicKeyBytes = ed25519.PublicKeySize

// SignatureBytes Ed25519 签名固定 64 字节。
const SignatureBytes = ed25519.SignatureSize

// PrivateKeyBytes Ed25519 私钥固定 64 字节（seed 32 + public 32）。
const PrivateKeyBytes = ed25519.PrivateKeySize

// Signer 目录签名器
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner 用 base64 编码的私钥创建签名器。
//
// 私钥格式（两种都接受，都是前端 `scripts/sign-catalog.mjs keygen` 的风格）：
//   - 64 字节的完整私钥（seed + public）
//   - 32 字节的 seed（会自动派生出 public）
//
// 长度不对一律报错，不做「补零」「截断」这类宽容匹配 ——
// 宽容匹配会让一个配错的私钥签出**验不过**的目录，
// 而症状是「所有游戏都装不上」，排查起来会去怀疑网络。
func NewSigner(privateKeyB64 string) (*Signer, error) {
	if privateKeyB64 == "" {
		return nil, errors.New("ed25519 私钥为空：不能签名（fail closed）")
	}
	raw, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		// 兼容 URL-safe base64（有些工具默认输出这种）
		raw, err = base64.RawURLEncoding.DecodeString(privateKeyB64)
		if err != nil {
			return nil, fmt.Errorf("ed25519 私钥不是合法 base64: %w", err)
		}
	}

	switch len(raw) {
	case PrivateKeyBytes:
		return &Signer{priv: ed25519.PrivateKey(raw)}, nil
	case ed25519.SeedSize:
		return &Signer{priv: ed25519.NewKeyFromSeed(raw)}, nil
	default:
		return nil, fmt.Errorf(
			"ed25519 私钥长度必须是 %d（完整私钥）或 %d（seed）字节，实际 %d 字节",
			PrivateKeyBytes, ed25519.SeedSize, len(raw))
	}
}

// Sign 对消息签名，返回**原始 64 字节**。
//
// ⚠️ 不要再套一层 SHA-256：Ed25519 内部用的是 SHA-512，
// 先哈希再签会让客户端的 `crypto.subtle.verify` 验不过。
func (s *Signer) Sign(message []byte) []byte {
	return ed25519.Sign(s.priv, message)
}

// SignToBase64 签名并编码成**单行 base64**（`.sig` 文件的内容）。
//
// 用 StdEncoding（不是 URL-safe、无换行）—— 前端用 `atob` 解，
// 换行会让它解出垃圾。
func (s *Signer) SignToBase64(message []byte) string {
	return base64.StdEncoding.EncodeToString(s.Sign(message))
}

// PublicKey 返回原始 32 字节公钥（用于确认配的和前端烧进去的是同一对）
func (s *Signer) PublicKey() []byte {
	return s.priv.Public().(ed25519.PublicKey)
}

// PublicKeyBase64 公钥的 base64（粘到前端 `catalogKey.ts` 用）
func (s *Signer) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(s.PublicKey())
}

// Verify 用 base64 公钥验签（服务端自检用，以及测试）。
func Verify(publicKeyB64 string, message, signature []byte) error {
	raw, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return fmt.Errorf("公钥不是合法 base64: %w", err)
	}
	if len(raw) != PublicKeyBytes {
		return fmt.Errorf("公钥必须是 %d 字节，实际 %d", PublicKeyBytes, len(raw))
	}
	if len(signature) != SignatureBytes {
		return fmt.Errorf("签名必须是 %d 字节，实际 %d", SignatureBytes, len(signature))
	}
	if !ed25519.Verify(ed25519.PublicKey(raw), message, signature) {
		return errors.New("ed25519 验签失败")
	}
	return nil
}

// VerifyRaw 用**原始 32 字节**公钥验签（服务端自检用）。
func VerifyRaw(publicKey, message, signature []byte) error {
	if len(publicKey) != PublicKeyBytes {
		return fmt.Errorf("公钥必须是 %d 字节，实际 %d", PublicKeyBytes, len(publicKey))
	}
	if len(signature) != SignatureBytes {
		return fmt.Errorf("签名必须是 %d 字节，实际 %d", SignatureBytes, len(signature))
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("ed25519 验签失败")
	}
	return nil
}

// VerifyBase64 用单行 base64 签名验签
func VerifyBase64(publicKeyB64 string, message []byte, signatureB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("签名不是合法 base64: %w", err)
	}
	return Verify(publicKeyB64, message, sig)
}

// GenerateKeyPair 生成一对密钥（发布者一次性使用）。
//
// 返回 (私钥 base64, 公钥 base64)。
//
// ⚠️ 私钥**只落一份**、放密钥管理服务或环境变量，**绝不提交进仓库**。
// 公钥粘到前端 `catalogKey.ts` 并提交。
func GenerateKeyPair() (privB64, pubB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("生成 ed25519 密钥失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv),
		base64.StdEncoding.EncodeToString(pub), nil
}
