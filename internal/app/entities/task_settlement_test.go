package entities

import (
	"testing"
	"time"
)

// TestSettlement 验证结算规则（对齐 kid-quest-farm 的 settlement.test.ts）
func TestSettlement(t *testing.T) {
	cases := []struct {
		name       string
		in         SettlementInput
		wantPoints int
		wantStatus TaskStatus
	}{
		{
			name:       "按时完成，全额",
			in:         SettlementInput{BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 30},
			wantPoints: 10,
			wantStatus: StatusCompleted,
		},
		{
			name:       "提前完成，仍全额",
			in:         SettlementInput{BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 10},
			wantPoints: 10,
			wantStatus: StatusCompleted,
		},
		{
			name: "超时 50%，衰减到一半",
			// 10 × (2 - 45/30) = 10 × 0.5 = 5
			in:         SettlementInput{BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 45, AllowOvertime: true},
			wantPoints: 5,
			wantStatus: StatusCompleted,
		},
		{
			name:       "超时到 2 倍，归零",
			in:         SettlementInput{BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 60, AllowOvertime: true},
			wantPoints: 0,
			wantStatus: StatusFailed,
		},
		{
			name:       "严格限时，超时即 0 分",
			in:         SettlementInput{BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 31, AllowOvertime: false},
			wantPoints: 0,
			wantStatus: StatusFailed,
		},
		{
			name:       "免扣分，超时也全额",
			in:         SettlementInput{BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 300, AllowLateNoPenalty: true},
			wantPoints: 10,
			wantStatus: StatusCompleted,
		},
		{
			name: "质量评级 great +20%",
			// 10 × 1.0（时间）× 1.2（质量）= 12
			in: SettlementInput{
				BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 20,
				QualityRated: true, Quality: GradeGreat,
			},
			wantPoints: 12,
			wantStatus: StatusCompleted,
		},
		{
			name: "质量评级 awful 归零",
			in: SettlementInput{
				BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 20,
				QualityRated: true, Quality: GradeAwful,
			},
			wantPoints: 0,
			wantStatus: StatusFailed,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Settle(c.in)
			if got.Points != c.wantPoints {
				t.Fatalf("points = %d, want %d (ratio=%.2f)", got.Points, c.wantPoints, got.TimeRatio)
			}
			if got.Status != c.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, c.wantStatus)
			}
		})
	}
}

// TestSettlementMinOnePoint 验证「差几秒就颗粒无收」的保护
func TestSettlementMinOnePoint(t *testing.T) {
	// 计划 30，实际 59（还没到 2 倍）
	// ratio = 2 - 59/30 = 0.0333 → 10 × 0.0333 = 0.33 → round = 0
	// 但 ratio > 0，所以应保底 1 分
	got := Settle(SettlementInput{
		BasePoints: 10, PlannedMinutes: 30, ActualMinutes: 59, AllowOvertime: true,
	})
	if got.Points != MinPointsBeforeZero {
		t.Fatalf("expected min 1 point, got %d", got.Points)
	}
}

// TestStatusMachine 验证状态机
func TestStatusMachine(t *testing.T) {
	cases := []struct {
		from, to TaskStatus
		allowed  bool
	}{
		{StatusPending, StatusSubmitted, true},
		{StatusPending, StatusExpired, true},
		{StatusPending, StatusSkipped, true},
		{StatusPending, StatusCompleted, false}, // 不能跳过审核
		{StatusSubmitted, StatusCompleted, true},
		{StatusSubmitted, StatusFailed, true},
		{StatusSubmitted, StatusRejected, true},
		{StatusRejected, StatusSubmitted, true},
		{StatusRejected, StatusSkipped, true},
		{StatusCompleted, StatusSubmitted, false}, // 终态
		{StatusFailed, StatusPending, false},
		{StatusExpired, StatusSubmitted, false},
	}

	for _, c := range cases {
		got := c.from.CanTransitionTo(c.to)
		if got != c.allowed {
			t.Fatalf("%s → %s: got %v, want %v", c.from, c.to, got, c.allowed)
		}
	}
}

// TestPeriodKey 验证周期键计算
func TestPeriodKey(t *testing.T) {
	// 2026-09-12 是周六，ISO 第 37 周
	d := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)

	if got := PeriodKey(CycleDaily, d); got != "2026-09-12" {
		t.Fatalf("daily = %s", got)
	}
	if got := PeriodKey(CycleMonthly, d); got != "2026-09" {
		t.Fatalf("monthly = %s", got)
	}
	if got := PeriodKey(CycleYearly, d); got != "2026" {
		t.Fatalf("yearly = %s", got)
	}
	if got := PeriodKey(CycleOnce, d); got != "once" {
		t.Fatalf("once = %s", got)
	}
	weekly := PeriodKey(CycleWeekly, d)
	if weekly != "2026-W37" {
		t.Fatalf("weekly = %s, want 2026-W37", weekly)
	}
}

// TestRunningMinutes 验证计时计算（含暂停）
func TestRunningMinutes(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	startedAt := now.Add(-30 * time.Minute).Unix()

	// 没暂停：30 分钟
	if got := RunningMinutes(startedAt, nil, 0, now); got != 30 {
		t.Fatalf("no pause: got %d, want 30", got)
	}

	// 累计暂停 10 分钟：20 分钟
	if got := RunningMinutes(startedAt, nil, 10*60*1000, now); got != 20 {
		t.Fatalf("paused 10min: got %d, want 20", got)
	}

	// 当前正在暂停 5 分钟（从 25 分钟前开始暂停）
	pausedAt := now.Add(-5 * time.Minute).Unix()
	if got := RunningMinutes(startedAt, &pausedAt, 0, now); got != 25 {
		t.Fatalf("pausing 5min: got %d, want 25", got)
	}
}

// TestQualityMultiplier 验证质量系数
func TestQualityMultiplier(t *testing.T) {
	cases := map[QualityGrade]float64{
		GradeAwful: 0.0,
		GradePoor:  0.5,
		GradeOK:    1.0,
		GradeGood:  1.1,
		GradeGreat: 1.2,
	}
	for g, want := range cases {
		if got := g.Multiplier(); got != want {
			t.Fatalf("%s multiplier = %.2f, want %.2f", g, got, want)
		}
	}
}
