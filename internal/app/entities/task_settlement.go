package entities

import (
	"fmt"
	"math"
	"time"
)

// ============================ 周期键 ============================

// PeriodKey 按周期计算归属键。
//
//	daily   → 2026-09-12
//	weekly  → 2026-W37
//	monthly → 2026-09
//	yearly  → 2026
//	once    → once（不随时间变化）
//
// 与 kid-quest-farm 的 `domain/time.ts` 语义一致。
func PeriodKey(cycle TaskCycle, t time.Time) string {
	switch cycle {
	case CycleDaily:
		return t.Format("2006-01-02")
	case CycleWeekly:
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case CycleMonthly:
		return t.Format("2006-01")
	case CycleYearly:
		return t.Format("2006")
	default: // once
		return "once"
	}
}

// DateString 返回 yyyy-MM-dd
func DateString(t time.Time) string {
	return t.Format("2006-01-02")
}

// ============================ 结算 ============================

// 结算常量（与 kid-quest-farm 的 settlement.ts 对齐）
const (
	// MinPointsBeforeZero 只要还没到归零阈值，至少保留 1 分
	// —— 避免"差几秒就颗粒无收"挫伤积极性
	MinPointsBeforeZero = 1
	// OvertimeZeroRatio 超时达到计划时长的这个倍数就归零
	OvertimeZeroRatio = 2.0
)

// SettlementInput 结算输入
type SettlementInput struct {
	// BasePoints 基础积分
	BasePoints int
	// PlannedMinutes 计划时长
	PlannedMinutes int
	// ActualMinutes 实际用时
	ActualMinutes int
	// AllowOvertime 允许超时结算
	AllowOvertime bool
	// AllowLateNoPenalty 免扣分
	AllowLateNoPenalty bool
	// QualityRated 是否启用质量评级
	QualityRated bool
	// Quality 质量评级（QualityRated 为 true 时有效）
	Quality QualityGrade
}

// SettlementResult 结算结果
type SettlementResult struct {
	// Points 最终得分
	Points int
	// TimeRatio 时间系数（1.0 = 全额，0 = 归零）
	TimeRatio float64
	// QualityMultiplier 质量系数
	QualityMultiplier float64
	// Status 结算后的状态：completed（有分）/ failed（零分）
	Status TaskStatus
	// Reason 说明（给孩子看的）
	Reason string
}

// Settle 计算任务得分。
//
// 规则（对齐 kid-quest-farm `domain/settlement.ts`）：
//
//	实际用时 <= 计划时长              → 全额
//	计划时长 < 实际用时 < 计划时长×2  → 按比例线性衰减
//	实际用时 >= 计划时长×2            → 0 分
//
// 例外：
//   - AllowLateNoPenalty = true → 无论超时多久都全额
//   - AllowOvertime = false     → 一旦超时就 0 分（严格限时任务）
//
// 衰减公式：得分 = round(基础分 × (2 - 实际用时 ÷ 计划用时))
func Settle(in SettlementInput) SettlementResult {
	// 计划时长为 0 或负：按全额给（避免除零）
	if in.PlannedMinutes <= 0 {
		return SettlementResult{
			Points:            applyQuality(in.BasePoints, in),
			TimeRatio:         1.0,
			QualityMultiplier: qualityMul(in),
			Status:            statusFor(in.BasePoints, in),
			Reason:            "完成",
		}
	}

	actual := in.ActualMinutes
	if actual < 0 {
		actual = 0
	}

	// 免扣分：全额
	if in.AllowLateNoPenalty {
		pts := applyQuality(in.BasePoints, in)
		return SettlementResult{
			Points:            pts,
			TimeRatio:         1.0,
			QualityMultiplier: qualityMul(in),
			Status:            statusFor(pts, in),
			Reason:            "按时完成",
		}
	}

	// 严格限时：超时即 0 分
	if !in.AllowOvertime && actual > in.PlannedMinutes {
		return SettlementResult{
			Points:            0,
			TimeRatio:         0,
			QualityMultiplier: qualityMul(in),
			Status:            StatusFailed,
			Reason:            "超时了",
		}
	}

	// 按比例衰减
	var ratio float64
	if actual <= in.PlannedMinutes {
		ratio = 1.0
	} else {
		ratio = OvertimeZeroRatio - float64(actual)/float64(in.PlannedMinutes)
		if ratio < 0 {
			ratio = 0
		}
	}

	pts := int(math.Round(float64(in.BasePoints) * ratio))
	// 还没到归零阈值，至少保留 1 分
	if pts <= 0 && ratio > 0 {
		pts = MinPointsBeforeZero
	}

	pts = applyQuality(pts, in)

	reason := "按时完成"
	switch {
	case ratio >= 1.0:
		reason = "按时完成"
	case ratio > 0:
		reason = "有点慢，但还是完成了"
	default:
		reason = "超时太久了"
	}

	return SettlementResult{
		Points:            pts,
		TimeRatio:         ratio,
		QualityMultiplier: qualityMul(in),
		Status:            statusFor(pts, in),
		Reason:            reason,
	}
}

// applyQuality 应用质量系数
func applyQuality(base int, in SettlementInput) int {
	if !in.QualityRated || in.Quality == "" {
		return base
	}
	mul := in.Quality.Multiplier()
	return int(math.Round(float64(base) * mul))
}

// qualityMul 返回质量系数（未启用时为 1.0）
func qualityMul(in SettlementInput) float64 {
	if !in.QualityRated || in.Quality == "" {
		return 1.0
	}
	return in.Quality.Multiplier()
}

// statusFor 根据得分决定状态
func statusFor(points int, in SettlementInput) TaskStatus {
	if points > 0 {
		return StatusCompleted
	}
	return StatusFailed
}

// ============================ 计时 ============================

// RunningMinutes 计算实际用时（分钟）。
//
//	实际用时 = now - startedAt - pausedTotalMs - (暂停中 ? now - pausedAt : 0)
//
// **所有算用时的地方都必须走它** —— 各写各的减法迟早会漏掉一处
// （tick 自动收尾、提交兜底、秒表、卡片）。
func RunningMinutes(startedAt int64, pausedAt *int64, pausedTotalMs int64, now time.Time) int {
	if startedAt <= 0 {
		return 0
	}
	nowMs := now.UnixMilli()
	elapsed := nowMs - startedAt*1000 - pausedTotalMs
	if pausedAt != nil && *pausedAt > 0 {
		elapsed -= nowMs - (*pausedAt * 1000)
	}
	if elapsed < 0 {
		return 0
	}
	return int(elapsed / 60000)
}
