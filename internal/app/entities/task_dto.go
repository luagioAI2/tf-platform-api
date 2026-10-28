package entities

// ============================ 任务请求 ============================

// CreateTaskReq 创建任务
type CreateTaskReq struct {
	Title              string          `json:"title" binding:"required,max=64"`
	Note               string          `json:"note" binding:"omitempty,max=255"`
	Category           TaskCategory    `json:"category" binding:"required,oneof=study chore sport art reading habit"`
	Cycle              TaskCycle       `json:"cycle" binding:"required,oneof=once daily weekly monthly yearly"`
	PlannedMinutes     int             `json:"planned_minutes" binding:"required,min=1,max=1440"`
	BasePoints         int             `json:"base_points" binding:"required,min=1,max=10000"`
	AllowOvertime      bool            `json:"allow_overtime"`
	AllowLateNoPenalty bool            `json:"allow_late_no_penalty"`
	QualityRated       bool            `json:"quality_rated"`
	RewardItemIDs      JSONStringArray `json:"reward_item_ids"`
	WindowStartMinute  *int            `json:"window_start_minute" binding:"omitempty,min=0,max=1439"`
	WindowEndMinute    *int            `json:"window_end_minute" binding:"omitempty,min=0,max=1439"`
	CheckInEnabled     bool            `json:"check_in_enabled"`
	CheckInTargetCount int             `json:"check_in_target_count" binding:"omitempty,min=0,max=365"`
	RemindAt           string          `json:"remind_at" binding:"omitempty,len=5"`
	Sort               int             `json:"sort"`
}

// UpdateTaskReq 更新任务（所有字段可选）
type UpdateTaskReq struct {
	Title              *string       `json:"title" binding:"omitempty,max=64"`
	Note               *string       `json:"note" binding:"omitempty,max=255"`
	Category           *TaskCategory `json:"category" binding:"omitempty"`
	PlannedMinutes     *int          `json:"planned_minutes" binding:"omitempty,min=1,max=1440"`
	BasePoints         *int          `json:"base_points" binding:"omitempty,min=1,max=10000"`
	AllowOvertime      *bool         `json:"allow_overtime"`
	AllowLateNoPenalty *bool         `json:"allow_late_no_penalty"`
	QualityRated       *bool         `json:"quality_rated"`
	RemindAt           *string       `json:"remind_at" binding:"omitempty,max=8"`
	Archived           *bool         `json:"archived"`
	Sort               *int          `json:"sort"`
}

// SubmitTaskReq 提交任务
type SubmitTaskReq struct {
	InstanceID uint `json:"instance_id" binding:"required,min=1"`
	// ActualMinutes 实际用时（分钟）。不传则按计时器算
	ActualMinutes int `json:"actual_minutes" binding:"omitempty,min=0,max=10080"`
	// Backfilled 是否当日补记
	Backfilled bool `json:"backfilled"`
}

// ReviewTaskReq 审核任务
type ReviewTaskReq struct {
	InstanceID uint         `json:"instance_id" binding:"required,min=1"`
	Approve    bool         `json:"approve"`
	Quality    QualityGrade `json:"quality" binding:"omitempty,oneof=awful poor ok good great"`
	Note       string       `json:"note" binding:"omitempty,max=255"`
}

// TimerReq 计时操作
type TimerReq struct {
	InstanceID uint `json:"instance_id" binding:"required,min=1"`
}

// ListTaskInstanceReq 查询任务实例
type ListTaskInstanceReq struct {
	Paginator
	TaskID    uint     `json:"task_id"`
	PeriodKey string   `json:"period_key" binding:"omitempty,max=16"`
	Date      string   `json:"date" binding:"omitempty,len=10"`
	DateFrom  string   `json:"date_from" binding:"omitempty,len=10"`
	DateTo    string   `json:"date_to" binding:"omitempty,len=10"`
	Status    []string `json:"status"`
}

// TaskBoardResp 今日任务板
type TaskBoardResp struct {
	Date        string          `json:"date"`
	Total       int             `json:"total"`
	Completed   int             `json:"completed"`
	Pending     int             `json:"pending"`
	Submitted   int             `json:"submitted"`
	TodayPoints int64           `json:"today_points"`
	Balance     int64           `json:"balance"`
	Instances   []*TaskInstance `json:"instances"`
}

// ============================ 签到 ============================

// CheckInReq 签到
type CheckInReq struct {
	TaskID uint `json:"task_id" binding:"required,min=1"`
}

// CheckInResp 签到结果
type CheckInResp struct {
	TaskID         uint   `json:"task_id"`
	Date           string `json:"date"`
	ContinuousDays int    `json:"continuous_days"`
	Points         int    `json:"points"`
	// NeedReview 是否需要家长审核（true 时暂不发分）
	NeedReview bool `json:"need_review"`
	// ClaimedTiers 本次新领取的阶梯奖励
	ClaimedTiers []int `json:"claimed_tiers"`
}

// CheckInStatusResp 签到状态
type CheckInStatusResp struct {
	TaskID         uint     `json:"task_id"`
	Title          string   `json:"title"`
	PeriodKey      string   `json:"period_key"`
	CheckedDays    []string `json:"checked_days"`
	PendingDays    []string `json:"pending_days"`
	TargetCount    int      `json:"target_count"`
	ContinuousDays int      `json:"continuous_days"`
	ClaimedTiers   []int    `json:"claimed_tiers"`
	// TodayChecked 今天是否已签到（含待审核）
	TodayChecked bool `json:"today_checked"`
}

// ============================ 游戏存储 ============================

// GameKvSetReq 写入游戏存档
type GameKvSetReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	Key    string `json:"key" binding:"required,max=128"`
	// Value 任意 JSON
	Value interface{} `json:"value" binding:"required"`
	// ExpectedVersion 乐观锁版本号（>0 时启用）
	ExpectedVersion int64 `json:"expected_version"`
}

// GameKvGetReq 读取游戏存档
type GameKvGetReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	Key    string `json:"key" binding:"required,max=128"`
}

// GameKvListReq 列出游戏存档
type GameKvListReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
}

// GameKvDeleteReq 删除游戏存档
type GameKvDeleteReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	Key    string `json:"key" binding:"omitempty,max=128"`
}

// ============================ 游戏币 ============================

// GameCoinInfo 游戏币余额
type GameCoinInfo struct {
	GameID      string `json:"game_id"`
	Balance     int64  `json:"balance"`
	TotalEarned int64  `json:"total_earned"`
	TotalSpent  int64  `json:"total_spent"`
}

// GameCoinBuyReq 用积分买游戏币
type GameCoinBuyReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	Count  int64  `json:"count" binding:"required,min=1,max=1000"`
	// PointsPerCoin 每个币多少积分（不传则用游戏配置）
	PointsPerCoin   int64  `json:"points_per_coin" binding:"omitempty,min=1"`
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// GameCoinSpendReq 消费游戏币
type GameCoinSpendReq struct {
	GameID          string `json:"game_id" binding:"required,max=32"`
	Amount          int64  `json:"amount" binding:"required,min=1,max=1000"`
	Memo            string `json:"memo" binding:"omitempty,max=128"`
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// GameCoinMintReq 铸币（插件调用）
type GameCoinMintReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	Amount int64  `json:"amount" binding:"required,min=1,max=1000"`
	Memo   string `json:"memo" binding:"omitempty,max=128"`
}

// GameCoinRedeemReq 游戏币换兑换券
type GameCoinRedeemReq struct {
	GameID          string `json:"game_id" binding:"required,max=32"`
	Count           int64  `json:"count" binding:"required,min=1,max=1000"`
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// GameCoinResp 游戏币操作结果
type GameCoinResp struct {
	GameID  string `json:"game_id"`
	Balance int64  `json:"balance"`
	// Amount 本次变动量
	Amount int64 `json:"amount"`
	// Vouchers 本次发出的兑换券（仅 redeem）
	Vouchers int64 `json:"vouchers,omitempty"`
	// Points 本次扣的积分（仅 buy）
	Points int64 `json:"points,omitempty"`
}
