package ed25519sign

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestSignVerifyRoundTrip 验证签名-验签闭环。
//
// 这是整个游戏下载链路的**信任根**：
// 服务端签目录、客户端用烧进 APK 的公钥验。
// 这条链断了，症状是「所有游戏都装不上」——
// 排查的人会先去怀疑网络，所以必须在服务端就钉死。
func TestSignVerifyRoundTrip(t *testing.T) {
	privB64, pubB64, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	signer, err := NewSigner(privB64)
	if err != nil {
		t.Fatal(err)
	}

	// 生成的公钥和从私钥派生的公钥必须是同一串
	if signer.PublicKeyBase64() != pubB64 {
		t.Fatalf("derived public key mismatch:\n got %s\nwant %s",
			signer.PublicKeyBase64(), pubB64)
	}

	message := []byte(`{"version":1,"games":[{"id":"cloudfarm","version":"1.1.4"}]}`)
	sig := signer.Sign(message)

	if len(sig) != SignatureBytes {
		t.Fatalf("signature length = %d, want %d", len(sig), SignatureBytes)
	}

	// 验签通过
	if err := Verify(pubB64, message, sig); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// base64 签名也能验
	if err := VerifyBase64(pubB64, message, signer.SignToBase64(message)); err != nil {
		t.Fatalf("VerifyBase64 failed: %v", err)
	}
}

// TestVerifyRejectsTampered 验证内容被改会验签失败
//
// 这是签名存在的唯一理由：中间人改了目录，客户端必须发现。
func TestVerifyRejectsTampered(t *testing.T) {
	privB64, pubB64, _ := GenerateKeyPair()
	signer, _ := NewSigner(privB64)

	original := []byte(`{"version":1,"games":[]}`)
	sig := signer.Sign(original)

	tampered := []byte(`{"version":1,"games":[{"id":"evil","url":"http://evil.example/x.mjs"}]}`)

	if err := Verify(pubB64, tampered, sig); err == nil {
		t.Fatal("tampered content MUST fail verification")
	}
}

// TestVerifyRejectsWrongKey 验证换一把公钥就验不过
func TestVerifyRejectsWrongKey(t *testing.T) {
	privA, _, _ := GenerateKeyPair()
	_, pubB, _ := GenerateKeyPair()

	signerA, _ := NewSigner(privA)
	message := []byte("hello")
	sig := signerA.Sign(message)

	if err := Verify(pubB, message, sig); err == nil {
		t.Fatal("verification with a different public key MUST fail")
	}
}

// TestSignToBase64IsSingleLine 验证签名是**单行** base64。
//
// 前端用 `atob` 解 `.sig` 文件内容 —— 换行会让它解出垃圾，
// 而症状同样是「所有游戏装不上」。
func TestSignToBase64IsSingleLine(t *testing.T) {
	privB64, _, _ := GenerateKeyPair()
	signer, _ := NewSigner(privB64)

	sig := signer.SignToBase64([]byte("test"))
	if strings.ContainsAny(sig, "\n\r") {
		t.Fatalf("signature must be single-line base64, got %q", sig)
	}
	// 必须是标准 base64（不是 URL-safe）
	if strings.ContainsAny(sig, "-_") {
		t.Fatalf("signature must use std base64 alphabet, got %q", sig)
	}
	if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
		t.Fatalf("signature is not valid std base64: %v", err)
	}
	if len(sig) != 88 { // 64 bytes → 88 chars (with padding)
		t.Fatalf("base64 of 64 bytes should be 88 chars, got %d", len(sig))
	}
}

// TestNewSignerAcceptsSeedAndFullKey 验证 seed(32) 和完整私钥(64) 都能用
func TestNewSignerAcceptsSeedAndFullKey(t *testing.T) {
	privB64, pubB64, _ := GenerateKeyPair()

	raw, _ := base64.StdEncoding.DecodeString(privB64)
	if len(raw) != PrivateKeyBytes {
		t.Fatalf("generated private key should be %d bytes, got %d", PrivateKeyBytes, len(raw))
	}

	seedB64 := base64.StdEncoding.EncodeToString(raw[:32])

	full, err := NewSigner(privB64)
	if err != nil {
		t.Fatalf("full key rejected: %v", err)
	}
	seed, err := NewSigner(seedB64)
	if err != nil {
		t.Fatalf("seed rejected: %v", err)
	}

	// 两者派生的公钥必须一致
	if full.PublicKeyBase64() != seed.PublicKeyBase64() {
		t.Fatalf("seed and full key derived different public keys")
	}
	if full.PublicKeyBase64() != pubB64 {
		t.Fatal("derived public key mismatch")
	}
}

// TestNewSignerFailsClosed 验证配置错误一律报错（不静默降级）
//
// ★ 关键安全属性：**绝不允许「没配就跳过签名」**。
// 那等于让「谁拿下静态服务器，谁就能在孩子手机里跑任意代码」
// 变成默认行为。
func TestNewSignerFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"空字符串", ""},
		{"不是 base64", "!!!not-base64!!!"},
		{"长度不对（太短）", base64.StdEncoding.EncodeToString([]byte("short"))},
		{"长度不对（太长）", base64.StdEncoding.EncodeToString(make([]byte, 128))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewSigner(c.key); err == nil {
				t.Fatal("invalid key MUST be rejected (fail closed)")
			}
		})
	}
}

// TestVerifyRejectsBadLengths 验证长度不对的公钥/签一律拒绝（不做宽容匹配）
func TestVerifyRejectsBadLengths(t *testing.T) {
	_, pubB64, _ := GenerateKeyPair()
	pubRaw, _ := base64.StdEncoding.DecodeString(pubB64)
	message := []byte("hello")

	// 签名长度不对
	if err := VerifyRaw(pubRaw, message, make([]byte, 32)); err == nil {
		t.Fatal("wrong signature length MUST be rejected")
	}
	// 公钥长度不对
	if err := VerifyRaw(make([]byte, 16), message, make([]byte, 64)); err == nil {
		t.Fatal("wrong public key length MUST be rejected")
	}
}
