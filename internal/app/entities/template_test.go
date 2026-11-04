package entities

import "testing"

func TestInviteCodeRoundTrip(t *testing.T) {
	for _, uid := range []uint{1, 2, 42, 1002, 65535, 123456789} {
		code := MakeInviteCode(uid)
		if code == "" {
			t.Fatalf("uid=%d 生成了空码", uid)
		}
		got, ok := ParseInviteCode(code)
		if !ok || got != uid {
			t.Fatalf("uid=%d 往返失败: code=%s got=%d ok=%v", uid, code, got, ok)
		}
	}
}

func TestInviteCodeRejectsTampered(t *testing.T) {
	good := MakeInviteCode(42)
	uid, ok := ParseInviteCode(good)
	if !ok || uid != 42 {
		t.Fatalf("好码被拒: %s", good)
	}
	// 改一位校验位 / 改 uid / 随便填：都必须拒，且 uid=0
	for _, bad := range []string{
		"TF16-0000", "TF16-DEAD", "TFZZ-ABCD", "hello", "", "TF0-0000", "TF16",
	} {
		if got, ok := ParseInviteCode(bad); ok || got != 0 {
			t.Fatalf("坏码被放行: %q -> uid=%d ok=%v", bad, got, ok)
		}
	}
	// 大小写 / 前后空格要能容忍（用户从微信里复制常带空格）
	if got, ok := ParseInviteCode("  " + MakeInviteCode(42) + "  "); !ok || got != 42 {
		t.Fatalf("带空格的码没被接受")
	}
	if MakeInviteCode(0) != "" {
		t.Fatal("uid=0 不该产出邀请码")
	}
}
