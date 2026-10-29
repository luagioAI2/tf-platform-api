package entities

import "testing"

// TestWishKindAllValid 验证七档分类的合法性。
//
// 上游 2026-10-29 新增了 `food` / `outing` 两档（用户原话
// 「收藏换成其他，前面再加美食，出行」），并给它们各配了样例商品。
// 服务端漏掉这两档的话，新商品会被**校验拒绝**，而且不报错到用户手里
// —— 表现为「后台加了商品但心愿页看不到」。
func TestWishKindAllValid(t *testing.T) {
	all := []WishKind{
		WishKindBlindbox,
		WishKindGamebox,
		WishKindToy,
		WishKindStudy,
		WishKindFood,
		WishKindOuting,
		WishKindCollectible,
	}
	for _, k := range all {
		if !k.IsValid() {
			t.Fatalf("%q 应该合法（上游 2026-10-29 的分类表）", k)
		}
	}
	if len(AllWishKinds()) != len(all) {
		t.Fatalf("AllWishKinds() 返回 %d 个，期望 %d 个", len(AllWishKinds()), len(all))
	}
	if WishKind("nope").IsValid() {
		t.Fatal("未知分类不该合法")
	}

	// `collectible` 的内部名**必须保留** —— 显示名从「收藏」改成「其他」，
	// 但改名会让存量数据对不上（上游 types.ts 的注释明确说了这条）
	if string(WishKindCollectible) != "collectible" {
		t.Fatal("collectible 的内部名不能改（存量数据用这个）")
	}
}

// TestWishKindDigital 验证数字商品判定没被新分类影响
func TestWishKindDigital(t *testing.T) {
	digital := map[WishKind]bool{
		WishKindBlindbox:    true,
		WishKindGamebox:     true,
		WishKindToy:         false,
		WishKindStudy:       false,
		WishKindFood:        false,
		WishKindOuting:      false,
		WishKindCollectible: false,
	}
	for k, want := range digital {
		if got := k.IsDigital(); got != want {
			t.Fatalf("%s.IsDigital() = %v, want %v", k, got, want)
		}
	}
}

// TestPendingPredicatesDiverge 验证两个「还没走完」的口径**只在一处分叉**。
//
// 上游 `6999530` 专门为这件事做了重构，并明确警告：
// 「同一个 committed 单子，心愿页说『有 1 单在等爸妈处理』、兑换记录没有徽标
//
//	—— **这不是 bug**，别拿一个去统一另一个。」
//
// 这条测试把「故意不相等」这件事**钉住**：谁要是把 ChildPending 改成
// 直接返回 NeedsParentAction，这里就红。
func TestPendingPredicatesDiverge(t *testing.T) {
	allStatuses := []WishStatus{
		WishStatusWished,
		WishStatusCommitted,
		WishStatusConfirmed,
		WishStatusDone,
		WishStatusCancelled,
	}

	for _, st := range allStatuses {
		// 未交付时两个口径必须一致
		if NeedsParentAction(st) != ChildPending(st, "") {
			t.Fatalf("status=%s 未交付时两个口径应一致", st)
		}
		// 已交付时：家长口径不变，孩子口径必须是 false
		if ChildPending(st, "cloudfarm") {
			t.Fatalf("status=%s 已交付时 ChildPending 必须是 false", st)
		}
	}

	// 只在「已交付的 committed/confirmed」上分叉
	if !NeedsParentAction(WishStatusCommitted) {
		t.Fatal("committed 家长还要收尾 → NeedsParentAction 应为 true")
	}
	if ChildPending(WishStatusCommitted, "cloudfarm") {
		t.Fatal("committed 且已交付 → ChildPending 应为 false" +
			"（否则记录页出现「红点 1 + 绿标签 已放进游戏柜」）")
	}

	// 变异检查：如果两个函数被写成同一个，上面这些断言会红。
	// 这里再明确一次「它们在且仅在一个条件上不同」。
	divergent := 0
	for _, st := range allStatuses {
		if NeedsParentAction(st) != ChildPending(st, "x") {
			divergent++
		}
	}
	if divergent != 2 { // committed / confirmed
		t.Fatalf("两个口径应该在 2 个状态上分叉，实际 %d 个", divergent)
	}
}

// TestMerchantRoleAndStatus 验证招商的角色与状态取值
func TestMerchantRoleAndStatus(t *testing.T) {
	for _, r := range []MerchantRole{MerchantRoleParent, MerchantRoleMerchant} {
		if !r.IsValid() {
			t.Fatalf("%q 应该合法", r)
		}
	}
	if MerchantRole("x").IsValid() {
		t.Fatal("未知角色不该合法")
	}

	// 客户端只认 pending / approved；服务端多一个 rejected（后台总得能拒）
	for _, s := range []MerchantStatus{
		MerchantStatusPending,
		MerchantStatusApproved,
		MerchantStatusRejected,
	} {
		if !s.IsValid() {
			t.Fatalf("%q 应该合法", s)
		}
		if MerchantStatusText[s] == "" {
			t.Fatalf("%q 缺少中文说法（后台列表要显示）", s)
		}
	}
}
