package entities

// ============================ 任务定义 ============================

// TaskCycle 任务周期
type TaskCycle string

const (
	CycleOnce    TaskCycle = "once"    // 一次性
	CycleDaily   TaskCycle = "daily"   // 每日
	CycleWeekly  TaskCycle = "weekly"  // 每周
	CycleMonthly TaskCycle = "monthly" // 每月
	CycleYearly  TaskCycle = "yearly"  // 每年
)

// IsValid 周期是否合法
func (c TaskCycle) IsValid() bool {
	switch c {
	case CycleOnce, CycleDaily, CycleWeekly, CycleMonthly, CycleYearly:
		return true
	}
	return false
}

// TaskCategory 任务分类（用于图标、配色、统计分组）
type TaskCategory string

const (
	CategoryStudy   TaskCategory = "study"   // 学习
	CategoryChore   TaskCategory = "chore"   // 家务
	CategorySport   TaskCategory = "sport"   // 运动
	CategoryArt     TaskCategory = "art"     // 艺术
	CategoryReading TaskCategory = "reading" // 阅读
	CategoryHabit   TaskCategory = "habit"   // 习惯
)

// IsValid 分类是否合法
func (c TaskCategory) IsValid() bool {
	switch c {
	case CategoryStudy, CategoryChore, CategorySport, CategoryArt, CategoryReading, CategoryHabit:
		return true
	}
	return false
}

// Task 任务定义（家长创建）。
//
// 对齐 kid-quest-farm-2d 的 `src/domain/types.ts` 的 Task 接口，
// 但做了服务端必要的裁剪与补充：
//   - 去掉了前端专用的可选覆盖字段（checkInTierRatio 等），改为任务级配置
//   - 加了 UID（多用户隔离）与软删除
type Task struct {
	BaseModel

	// UID 任务所属用户（家长）
	UID uint `gorm:"column:uid;index:idx_uid_cycle;not null" json:"uid"`

	// Title 任务标题，如「完成语文作业」
	Title string `gorm:"column:title;size:64;not null" json:"title"`
	// Note 补充说明
	Note string `gorm:"column:note;size:255" json:"note"`

	// Category 分类
	Category TaskCategory `gorm:"column:category;size:16;index;not null" json:"category"`
	// Cycle 周期
	Cycle TaskCycle `gorm:"column:cycle;size:16;index:idx_uid_cycle;not null" json:"cycle"`

	// PlannedMinutes 计划时长（分钟）—— 结算的核心依据
	//
	//	实际用时 <= 计划时长              → 全额积分
	//	计划时长 < 实际用时 < 计划时长×2  → 按比例线性衰减
	//	实际用时 >= 计划时长×2            → 0 分（除非 allowLateNoPenalty / allowOvertime）
	PlannedMinutes int `gorm:"column:planned_minutes;default:30;not null" json:"planned_minutes"`

	// BasePoints 基础积分
	BasePoints int `gorm:"column:base_points;default:10;not null" json:"base_points"`

	// AllowOvertime 是否允许超时结算
	//   false = 超时直接 0 分（严格的"限时任务"）
	//   true  = 超时按超时比例结算，超出计划时长一倍才归零
	AllowOvertime bool `gorm:"column:allow_overtime;default:false" json:"allow_overtime"`

	// AllowLateNoPenalty 免扣分：无论超时多久都不扣分，仍按全额结算
	AllowLateNoPenalty bool `gorm:"column:allow_late_no_penalty;default:false" json:"allow_late_no_penalty"`

	// QualityRated 是否启用质量评级（完成后家长/孩子选择质量）
	QualityRated bool `gorm:"column:quality_rated;default:false" json:"quality_rated"`

	// RewardItemIDs 奖励道具 id 列表（JSON 数组，完成时掉落）
	RewardItemIDs JSONStringArray `gorm:"column:reward_item_ids;type:text" json:"reward_item_ids"`

	// WindowStartMinute / WindowEndMinute 允许的完成时间段（分钟数，从 0:00 起算）
	// 用于「练字签到」等需要在特定时段完成的任务。nil 表示不限。
	WindowStartMinute *int `gorm:"column:window_start_minute" json:"window_start_minute"`
	WindowEndMinute   *int `gorm:"column:window_end_minute" json:"window_end_minute"`

	// CheckInEnabled 是否启用签到模式（同一周期内每天签到，按累计天数给阶梯奖励）
	CheckInEnabled bool `gorm:"column:check_in_enabled;default:false" json:"check_in_enabled"`
	// CheckInTargetCount 周期目标次数（如一周 5 天）。仅 CheckInEnabled 时有效
	CheckInTargetCount int `gorm:"column:check_in_target_count;default:0" json:"check_in_target_count"`

	// RemindAt 提醒时间（HH:mm），仅用于展示与本地通知
	RemindAt string `gorm:"column:remind_at;size:8" json:"remind_at"`

	// Archived 是否归档
	Archived bool `gorm:"column:archived;default:false;index" json:"archived"`

	// Sort 排序权重
	Sort int `gorm:"column:sort;default:0" json:"sort"`
}

func (Task) TableName() string { return "task" }

// ============================ 任务实例 ============================

// TaskStatus 任务实例状态
type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"   // 待完成
	StatusSubmitted TaskStatus = "submitted" // 已提交，等家长审核
	StatusCompleted TaskStatus = "completed" // 已通过（含超时但仍有分）
	StatusFailed    TaskStatus = "failed"    // 已通过但零分
	StatusExpired   TaskStatus = "expired"   // 过期未做
	StatusSkipped   TaskStatus = "skipped"   // 主动放弃
	StatusRejected  TaskStatus = "rejected"  // 家长打回，要重做
)

// IsValid 状态是否合法
func (s TaskStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusSubmitted, StatusCompleted, StatusFailed,
		StatusExpired, StatusSkipped, StatusRejected:
		return true
	}
	return false
}

// IsTerminal 是否终态（不可再变更）
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusExpired, StatusSkipped:
		return true
	}
	return false
}

// CanTransitionTo 状态机：是否允许从 s 跃迁到 target。
//
// 状态机（严格）：
//
//	pending   → submitted | expired | skipped
//	submitted → completed | failed | rejected
//	rejected  → submitted | skipped
//	completed / failed / expired / skipped → 终态
func (s TaskStatus) CanTransitionTo(target TaskStatus) bool {
	switch s {
	case StatusPending:
		return target == StatusSubmitted || target == StatusExpired || target == StatusSkipped
	case StatusSubmitted:
		return target == StatusCompleted || target == StatusFailed || target == StatusRejected
	case StatusRejected:
		return target == StatusSubmitted || target == StatusSkipped
	default:
		return false // 终态不可跃迁
	}
}

// QualityGrade 质量评级（五档，由低到高）
//
//	awful 很差   -100%
//	poor  一般   -50%
//	ok    良好     0%   ← 中间档，不加不减
//	good  很好   +10%
//	great 特别棒 +20%
type QualityGrade string

const (
	GradeAwful QualityGrade = "awful"
	GradePoor  QualityGrade = "poor"
	GradeOK    QualityGrade = "ok"
	GradeGood  QualityGrade = "good"
	GradeGreat QualityGrade = "great"
)

// IsValid 评级是否合法
func (g QualityGrade) IsValid() bool {
	switch g {
	case GradeAwful, GradePoor, GradeOK, GradeGood, GradeGreat:
		return true
	}
	return false
}

// Multiplier 评级对应的分数系数
func (g QualityGrade) Multiplier() float64 {
	switch g {
	case GradeAwful:
		return 0.0
	case GradePoor:
		return 0.5
	case GradeOK:
		return 1.0
	case GradeGood:
		return 1.1
	case GradeGreat:
		return 1.2
	default:
		return 1.0
	}
}

// TaskInstance 任务实例（每周期生成一条）。
//
// 唯一约束 `(uid, task_id, period_key, date)`：
//   - 日任务：periodKey 等于当天 → 一天一条
//   - 周期任务：同一天不能重复记一次，跨天可以记满 N 次
//
// 这个约束是从 kid-quest-farm 的 v4 迁移学来的：v2 用
// `&[taskId+periodKey]` 唯一索引，导致周期任务（如「一月读 4 本书」）
// 第二次提交必然撞唯一索引、抛 ConstraintError。
type TaskInstance struct {
	BaseModel

	UID    uint `gorm:"column:uid;uniqueIndex:uk_uid_task_period_date;index:idx_uid_status;not null" json:"uid"`
	TaskID uint `gorm:"column:task_id;uniqueIndex:uk_uid_task_period_date;index;not null" json:"task_id"`

	// PeriodKey 归属周期键：daily→2026-09-12；weekly→2026-W37；monthly→2026-09；yearly→2026
	PeriodKey string `gorm:"column:period_key;size:16;uniqueIndex:uk_uid_task_period_date;index;not null" json:"period_key"`
	// Date 计划日期（yyyy-MM-dd）
	Date string `gorm:"column:date;size:10;uniqueIndex:uk_uid_task_period_date;index;not null" json:"date"`

	// 以下字段是 Task 的快照（任务改了不影响已生成的实例）
	Title              string          `gorm:"column:title;size:64;not null" json:"title"`
	Category           TaskCategory    `gorm:"column:category;size:16" json:"category"`
	Cycle              TaskCycle       `gorm:"column:cycle;size:16" json:"cycle"`
	PlannedMinutes     int             `gorm:"column:planned_minutes;default:30" json:"planned_minutes"`
	BasePoints         int             `gorm:"column:base_points;default:10" json:"base_points"`
	AllowOvertime      bool            `gorm:"column:allow_overtime;default:false" json:"allow_overtime"`
	AllowLateNoPenalty bool            `gorm:"column:allow_late_no_penalty;default:false" json:"allow_late_no_penalty"`
	QualityRated       bool            `gorm:"column:quality_rated;default:false" json:"quality_rated"`
	RewardItemIDs      JSONStringArray `gorm:"column:reward_item_ids;type:text" json:"reward_item_ids"`

	// Status 当前状态
	Status TaskStatus `gorm:"column:status;size:16;default:'pending';index:idx_uid_status" json:"status"`

	// ActualMinutes 实际用时（分钟）
	ActualMinutes *int `gorm:"column:actual_minutes" json:"actual_minutes"`
	// EarnedPoints 本次实际获得积分（结算后写入）
	EarnedPoints *int `gorm:"column:earned_points" json:"earned_points"`
	// Quality 质量评级
	Quality string `gorm:"column:quality;size:16" json:"quality"`

	// StartedAt 计时开始时间戳（nil 表示还没开始计时）
	//
	// 低龄用户友好：点「开始」才开跑，避免"忘记点开始"导致误判超时。
	StartedAt *int64 `gorm:"column:started_at" json:"started_at"`
	// PausedAt 暂停时刻（有值 = 当前正处在暂停中）
	PausedAt *int64 `gorm:"column:paused_at" json:"paused_at"`
	// PausedTotalMs 累计已暂停的毫秒数（跨多次暂停累加）
	//
	// 计时真相源仍是 StartedAt，暂停只是从里面扣掉一段时间：
	//
	//	实际用时 = now - startedAt - pausedTotalMs - (暂停中 ? now - pausedAt : 0)
	PausedTotalMs int64 `gorm:"column:paused_total_ms;default:0" json:"paused_total_ms"`

	// SubmittedAt 提交时间戳
	SubmittedAt *int64 `gorm:"column:submitted_at" json:"submitted_at"`
	// SettledAt 结算时间戳
	SettledAt *int64 `gorm:"column:settled_at" json:"settled_at"`
	// CompletedAt 完成时间戳
	CompletedAt *int64 `gorm:"column:completed_at" json:"completed_at"`

	// Backfilled 是否当日补记（事后补做）
	Backfilled bool `gorm:"column:backfilled;default:false" json:"backfilled"`
	// RejectReason 家长打回原因
	RejectReason string `gorm:"column:reject_reason;size:255" json:"reject_reason"`
	// ReviewNote 家长审核备注
	ReviewNote string `gorm:"column:review_note;size:255" json:"review_note"`
}

func (TaskInstance) TableName() string { return "task_instance" }

// ============================ 签到 ============================

// CheckInRecord 签到记录（已通过家长审核的才写这里）
type CheckInRecord struct {
	BaseModel

	UID    uint `gorm:"column:uid;uniqueIndex:uk_uid_task_date;index;not null" json:"uid"`
	TaskID uint `gorm:"column:task_id;uniqueIndex:uk_uid_task_date;index;not null" json:"task_id"`

	// Date 签到日期 yyyy-MM-dd
	Date string `gorm:"column:date;size:10;uniqueIndex:uk_uid_task_date;index;not null" json:"date"`
	// PeriodKey 归属周期
	PeriodKey string `gorm:"column:period_key;size:16;index;not null" json:"period_key"`

	// Points 该次签到获得的积分
	Points int `gorm:"column:points;default:0" json:"points"`
}

func (CheckInRecord) TableName() string { return "check_in_record" }

// CheckInProgress 签到进度（每任务每周期一条）
type CheckInProgress struct {
	BaseModel

	UID       uint   `gorm:"column:uid;uniqueIndex:uk_uid_task_period;not null" json:"uid"`
	TaskID    uint   `gorm:"column:task_id;size:32;uniqueIndex:uk_uid_task_period;not null" json:"task_id"`
	PeriodKey string `gorm:"column:period_key;size:16;uniqueIndex:uk_uid_task_period;not null" json:"period_key"`

	// Days 已签到日期列表（**已通过家长审核**的才算）
	Days JSONStringArray `gorm:"column:days;type:text" json:"days"`
	// PendingDays 已提交、等家长审核的日期
	//
	// 为什么要有这一层：签到以前是「孩子一点就发分」，等于自己给自己发奖。
	// 开启家长审核后，点签到只把日期挪到这里 —— 不发积分、不写签到记录、
	// 不计入坚持天数、不触发阶梯奖励。家长确认后才移到 Days。
	PendingDays JSONStringArray `gorm:"column:pending_days;type:text" json:"pending_days"`
	// ClaimedTiers 已领取的阶梯奖励（天数阈值）
	ClaimedTiers JSONIntArray `gorm:"column:claimed_tiers;type:text" json:"claimed_tiers"`
}

func (CheckInProgress) TableName() string { return "check_in_progress" }
