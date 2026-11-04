package entities

import "testing"

// TestPlanRedeemPayment 验证兑换分账规则（券优先 + 1:1 + 可混付）
//
// 对齐 kid-quest-farm `domain/redeem.test.ts`。
func TestPlanRedeemPayment(t *testing.T) {
	cases := []struct {
		name                   string
		price, coupon, points  int64
		wantCoupon, wantPoints int64
		wantAffordable         bool
		wantShortfall          int64
	}{
		{
			name:  "券够，全用券",
			price: 50, coupon: 80, points: 100,
			wantCoupon: 50, wantPoints: 0,
			wantAffordable: true, wantShortfall: 0,
		},
		{
			name:  "券够但刚好，全用券",
			price: 50, coupon: 50, points: 100,
			wantCoupon: 50, wantPoints: 0,
			wantAffordable: true, wantShortfall: 0,
		},
		{
			name:  "券不够，剩下的用积分补",
			price: 50, coupon: 30, points: 100,
			wantCoupon: 30, wantPoints: 20,
			wantAffordable: true, wantShortfall: 0,
		},
		{
			name:  "只有积分，全用积分",
			price: 50, coupon: 0, points: 100,
			wantCoupon: 0, wantPoints: 50,
			wantAffordable: true, wantShortfall: 0,
		},
		{
			name:  "两种币加起来刚好够",
			price: 50, coupon: 30, points: 20,
			wantCoupon: 30, wantPoints: 20,
			wantAffordable: true, wantShortfall: 0,
		},
		{
			name:  "两种币加起来不够",
			price: 50, coupon: 30, points: 10,
			wantCoupon: 30, wantPoints: 20,
			wantAffordable: false, wantShortfall: 10,
		},
		{
			name:  "什么都没有",
			price: 50, coupon: 0, points: 0,
			wantCoupon: 0, wantPoints: 50,
			wantAffordable: false, wantShortfall: 50,
		},
		{
			name:  "负数兜底（余额传成负数当 0）",
			price: 50, coupon: -10, points: 100,
			wantCoupon: 0, wantPoints: 50,
			wantAffordable: true, wantShortfall: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlanRedeemPayment(c.price, c.coupon, c.points)
			if got.CouponPaid != c.wantCoupon {
				t.Fatalf("couponPaid = %d, want %d", got.CouponPaid, c.wantCoupon)
			}
			if got.PointsPaid != c.wantPoints {
				t.Fatalf("pointsPaid = %d, want %d", got.PointsPaid, c.wantPoints)
			}
			if got.Total != got.CouponPaid+got.PointsPaid {
				t.Fatalf("total %d != coupon %d + points %d",
					got.Total, got.CouponPaid, got.PointsPaid)
			}
			if got.Affordable != c.wantAffordable {
				t.Fatalf("affordable = %v, want %v", got.Affordable, c.wantAffordable)
			}
			if got.Shortfall != c.wantShortfall {
				t.Fatalf("shortfall = %d, want %d", got.Shortfall, c.wantShortfall)
			}
		})
	}
}

// TestDescribeRedeemPayment 验证文案（只出一种币时不写「+0」）
func TestDescribeRedeemPayment(t *testing.T) {
	cases := []struct {
		coupon, points int64
		want           string
	}{
		{30, 0, "30 兑换券"},
		{0, 50, "50 积分"},
		{30, 20, "30 兑换券 + 20 积分"},
	}
	for _, c := range cases {
		if got := DescribeRedeemPayment(c.coupon, c.points); got != c.want {
			t.Fatalf("DescribeRedeemPayment(%d,%d) = %q, want %q", c.coupon, c.points, got, c.want)
		}
	}
}

// TestBlindBoxRemaining 验证盲盒池（减去已拥有 + 去重）
func TestBlindBoxRemaining(t *testing.T) {
	pool := []string{"a", "b", "c", "d"}
	owned := []string{"b"}

	got := BlindBoxRemaining(pool, owned)
	if len(got) != 3 {
		t.Fatalf("remaining = %v, want 3 items", got)
	}
	for _, id := range got {
		if id == "b" {
			t.Fatal("owned game should not be in remaining pool")
		}
	}

	// 池子里同一个 id 写两次不能让它概率翻倍
	dupPool := []string{"a", "a", "b"}
	got2 := BlindBoxRemaining(dupPool, nil)
	if len(got2) != 2 {
		t.Fatalf("dedup failed: %v, want 2 items", got2)
	}
}

// TestPickBlindBoxMany 验证「5 连开」：不重复、不含已拥有、剩余不足时给短的
func TestPickBlindBoxMany(t *testing.T) {
	pool := []string{"a", "b", "c", "d", "e", "f", "g"}

	// ① 开 5 款：长度对、不重复、全在池子里
	got := PickBlindBoxMany(pool, nil, 5, 0.42)
	if len(got) != 5 {
		t.Fatalf("want 5, got %d (%v)", len(got), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate %q in %v", id, got)
		}
		seen[id] = true
	}

	// ② ★ 「已经解锁的不计算」—— 已拥有的不占名额，也不会被开出来
	owned := []string{"a", "b", "c"}
	got = PickBlindBoxMany(pool, owned, 4, 0.42)
	if len(got) != 4 {
		t.Fatalf("want 4 from remaining 4, got %d (%v)", len(got), got)
	}
	for _, id := range got {
		if id == "a" || id == "b" || id == "c" {
			t.Fatalf("开出了已拥有的 %q（%v）", id, got)
		}
	}

	// ③ 剩余不足 → 返回短的（调用方负责「整单拒绝」）
	if got = PickBlindBoxMany(pool, owned, 5, 0.42); len(got) != 4 {
		t.Fatalf("剩余 4 款要 5 款 → 应返回 4 条，得到 %d（%v）", len(got), got)
	}

	// ④ 全拥有 → 空（调用方必须**先判断**再扣券）
	if got = PickBlindBoxMany(pool, pool, 5, 0.42); len(got) != 0 {
		t.Fatalf("全拥有应为空，得到 %v", got)
	}

	// ⑤ n = 1 就是老的单开；rand 越界也不崩
	if got = PickBlindBoxMany(pool, nil, 1, 1.0); len(got) != 1 {
		t.Fatalf("n=1 rand=1.0 → 应给 1 条，得到 %v", got)
	}
	if got = PickBlindBoxMany(pool, nil, 0, 0.5); len(got) != 0 {
		t.Fatalf("n=0 应为空，得到 %v", got)
	}

	// ⑥ 确定性：同一个 randVal 必须给出同一批（测试与复现要靠它）
	first := PickBlindBoxMany(pool, nil, 5, 0.42)
	again := PickBlindBoxMany(pool, nil, 5, 0.42)
	if len(again) != 5 {
		t.Fatalf("again want 5, got %d", len(again))
	}
	for i := range first {
		if first[i] != again[i] {
			t.Fatalf("同样的 randVal 两次结果不同：%v vs %v", first, again)
		}
	}
}

// TestWishStatusMachine 验证心愿单状态机
func TestWishStatusMachine(t *testing.T) {
	cases := []struct {
		from, to WishStatus
		allowed  bool
	}{
		{WishStatusWished, WishStatusCommitted, true},
		{WishStatusWished, WishStatusCancelled, true},
		{WishStatusWished, WishStatusDone, false}, // 不能跳过兑换直接完成
		{WishStatusCommitted, WishStatusConfirmed, true},
		// 数字商品（盲盒/盒子）兑换当场交付，不必经过 confirmed
		{WishStatusCommitted, WishStatusDone, true},
		{WishStatusCommitted, WishStatusCancelled, true},
		{WishStatusConfirmed, WishStatusDone, true},
		{WishStatusConfirmed, WishStatusCancelled, true},
		{WishStatusDone, WishStatusCancelled, false},      // 终态
		{WishStatusCancelled, WishStatusCommitted, false}, // 终态
	}

	for _, c := range cases {
		got := c.from.CanTransitionTo(c.to)
		if got != c.allowed {
			t.Fatalf("%s → %s: got %v, want %v", c.from, c.to, got, c.allowed)
		}
	}
}

// TestWishStatusHasHeldVouchers 验证「券是否押在平台手里」
//
// 只有 committed / confirmed 可以取消退券：
// wished 还没扣、done/cancelled 已终结。
func TestWishStatusHasHeldVouchers(t *testing.T) {
	cases := map[WishStatus]bool{
		WishStatusWished:    false,
		WishStatusCommitted: true,
		WishStatusConfirmed: true,
		WishStatusDone:      false,
		WishStatusCancelled: false,
	}
	for status, want := range cases {
		if got := status.HasHeldVouchers(); got != want {
			t.Fatalf("%s.HasHeldVouchers() = %v, want %v", status, got, want)
		}
	}
}

// TestWishKindIsDigital 验证数字商品判定
func TestWishKindIsDigital(t *testing.T) {
	cases := map[WishKind]bool{
		WishKindBlindbox:    true,
		WishKindGamebox:     true,
		WishKindToy:         false,
		WishKindStudy:       false,
		WishKindCollectible: false,
	}
	for kind, want := range cases {
		if got := kind.IsDigital(); got != want {
			t.Fatalf("%s.IsDigital() = %v, want %v", kind, got, want)
		}
	}
}

// TestDaysToSave 验证「大约要攒几天」
func TestDaysToSave(t *testing.T) {
	cases := []struct {
		price, perDay int64
		want          int
	}{
		{100, 10, 10},
		{100, 30, 4}, // 向上取整
		{100, 100, 1},
		{100, 0, -1}, // 算不出 → -1（不是「∞ 天」）
		{100, -5, -1},
		{0, 10, 0}, // 免费
	}
	for _, c := range cases {
		if got := DaysToSave(c.price, c.perDay); got != c.want {
			t.Fatalf("DaysToSave(%d,%d) = %d, want %d", c.price, c.perDay, got, c.want)
		}
	}
}

// TestWishShipInfoMasked 验证收货信息脱敏（孩子端不能看到完整信息）
func TestWishShipInfoMasked(t *testing.T) {
	ship := &WishShipInfo{
		Receiver: "张三",
		Phone:    "13800138000",
		Region:   "浙江省杭州市",
		Address:  "西湖区某某路 1 号 101 室",
	}
	m := ship.Masked()
	if m.Phone != "****8000" {
		t.Fatalf("phone = %q, want ****8000", m.Phone)
	}
	if m.Address != "" {
		t.Fatalf("address should be empty for child view, got %q", m.Address)
	}
	if m.Region == "" {
		t.Fatal("region should be kept (needed to tell apart multiple addresses)")
	}

	// nil 安全
	var nilShip *WishShipInfo
	if nilShip.Masked() != nil {
		t.Fatal("nil ship should mask to nil")
	}
}
