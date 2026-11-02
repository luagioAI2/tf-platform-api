package service

import (
	"context"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var TaskServiceSet = wire.NewSet(ProvideTaskService)

// TaskService 任务服务：定义 CRUD、实例生成、计时、提交、审核、结算
type TaskService struct {
	TaskRepo  *repository.TaskRepository
	CheckRepo *repository.CheckInRepository
	WalletSrv *WalletService
	EventSrv  *EventService
	timezone  *time.Location
}

// ProvideTaskService 构造函数
func ProvideTaskService(
	taskRepo *repository.TaskRepository,
	checkRepo *repository.CheckInRepository,
	walletSrv *WalletService,
	eventSrv *EventService,
) *TaskService {
	loc, err := time.LoadLocation(config.Get().ServiceSettings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return &TaskService{
		TaskRepo:  taskRepo,
		CheckRepo: checkRepo,
		WalletSrv: walletSrv,
		EventSrv:  eventSrv,
		timezone:  loc,
	}
}

// Now 当前时间（配置时区）
func (s *TaskService) Now() time.Time { return time.Now().In(s.timezone) }

// ============================ 任务定义 ============================

// CreateTask 创建任务
func (s *TaskService) CreateTask(ctx context.Context, uid uint, req *entities.CreateTaskReq) (*entities.Task, error) {
	if !req.Cycle.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}
	if !req.Category.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}
	if req.PlannedMinutes <= 0 || req.BasePoints <= 0 {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	task := &entities.Task{
		UID:                uid,
		Title:              req.Title,
		Note:               req.Note,
		Category:           req.Category,
		Cycle:              req.Cycle,
		PlannedMinutes:     req.PlannedMinutes,
		BasePoints:         req.BasePoints,
		AllowOvertime:      req.AllowOvertime,
		AllowLateNoPenalty: req.AllowLateNoPenalty,
		QualityRated:       req.QualityRated,
		RewardItemIDs:      req.RewardItemIDs,
		WindowStartMinute:  req.WindowStartMinute,
		WindowEndMinute:    req.WindowEndMinute,
		CheckInEnabled:     req.CheckInEnabled,
		CheckInTargetCount: req.CheckInTargetCount,
		RemindAt:           req.RemindAt,
		Sort:               req.Sort,
	}
	if err := s.TaskRepo.CreateTask(ctx, task); err != nil {
		return nil, apperrors.WithError(err)
	}

	// 立即生成当前周期的实例（用户创建后就能看到）
	if err := s.ensureInstanceForTask(ctx, uid, task); err != nil {
		logger.ZWarn("ensure instance failed after create",
			zap.Uint("uid", uid), zap.Uint("task_id", task.ID), zap.Error(err))
	}
	return task, nil
}

// UpdateTask 更新任务
func (s *TaskService) UpdateTask(ctx context.Context, uid, taskID uint, req *entities.UpdateTaskReq) (*entities.Task, error) {
	task, err := s.TaskRepo.GetTask(ctx, uid, taskID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if task == nil {
		return nil, apperrors.WithCode(apperrors.TaskNotExist)
	}

	fields := make(map[string]interface{}, 12)
	if req.Title != nil {
		fields["title"] = *req.Title
	}
	if req.Note != nil {
		fields["note"] = *req.Note
	}
	if req.Category != nil {
		if !req.Category.IsValid() {
			return nil, apperrors.WithCode(apperrors.InvalidParam)
		}
		fields["category"] = string(*req.Category)
	}
	if req.PlannedMinutes != nil {
		if *req.PlannedMinutes <= 0 {
			return nil, apperrors.WithCode(apperrors.InvalidParam)
		}
		fields["planned_minutes"] = *req.PlannedMinutes
	}
	if req.BasePoints != nil {
		if *req.BasePoints <= 0 {
			return nil, apperrors.WithCode(apperrors.InvalidParam)
		}
		fields["base_points"] = *req.BasePoints
	}
	if req.AllowOvertime != nil {
		fields["allow_overtime"] = *req.AllowOvertime
	}
	if req.AllowLateNoPenalty != nil {
		fields["allow_late_no_penalty"] = *req.AllowLateNoPenalty
	}
	if req.QualityRated != nil {
		fields["quality_rated"] = *req.QualityRated
	}
	if req.RemindAt != nil {
		fields["remind_at"] = *req.RemindAt
	}
	if req.Archived != nil {
		fields["archived"] = *req.Archived
	}
	if req.Sort != nil {
		fields["sort"] = *req.Sort
	}

	if err := s.TaskRepo.UpdateTask(ctx, uid, taskID, fields); err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.TaskRepo.GetTask(ctx, uid, taskID)
}

// DeleteTask 删除任务
func (s *TaskService) DeleteTask(ctx context.Context, uid, taskID uint) error {
	task, err := s.TaskRepo.GetTask(ctx, uid, taskID)
	if err != nil {
		return apperrors.WithError(err)
	}
	if task == nil {
		return apperrors.WithCode(apperrors.TaskNotExist)
	}
	// ⚠️ 不能写 `return apperrors.WithError(repo.DeleteTask(...))`：
	// `WithError(nil)` 返回的是**非 nil** 的 500 错误对象（errors/error.go:127），
	// 于是「删成功」也会回 500 —— 数据已经删了，界面却报「系统错误」。
	if err := s.TaskRepo.DeleteTask(ctx, uid, taskID); err != nil {
		return apperrors.WithError(err)
	}
	return nil
}

// ListTasks 列出任务
func (s *TaskService) ListTasks(ctx context.Context, uid uint, includeArchived bool) ([]*entities.Task, error) {
	list, err := s.TaskRepo.ListTasks(ctx, uid, includeArchived)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return list, nil
}

// ============================ 实例生成 ============================

// Today 今天（配置时区的 yyyy-MM-dd）
func (s *TaskService) Today() string { return entities.DateString(s.Now()) }

// ensureInstanceForTask 为单个任务确保当前周期实例存在
func (s *TaskService) ensureInstanceForTask(ctx context.Context, uid uint, task *entities.Task) error {
	now := s.Now()
	periodKey := entities.PeriodKey(task.Cycle, now)
	date := entities.DateString(now)

	// 一次性任务：已经有实例就不再生成
	if task.Cycle == entities.CycleOnce {
		existing, _, err := s.TaskRepo.ListInstances(ctx, repository.InstanceQuery{
			UID: uid, TaskID: task.ID, Page: 1, PageSize: 1,
		})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return nil
		}
	}

	inst := &entities.TaskInstance{
		UID:                uid,
		TaskID:             task.ID,
		PeriodKey:          periodKey,
		Date:               date,
		Title:              task.Title,
		Category:           task.Category,
		Cycle:              task.Cycle,
		PlannedMinutes:     task.PlannedMinutes,
		BasePoints:         task.BasePoints,
		AllowOvertime:      task.AllowOvertime,
		AllowLateNoPenalty: task.AllowLateNoPenalty,
		QualityRated:       task.QualityRated,
		RewardItemIDs:      task.RewardItemIDs,
		Status:             entities.StatusPending,
	}
	return s.TaskRepo.CreateInstance(ctx, inst)
}

// EnsureTodayInstances 确保今天所有任务的实例都已生成（登录/打开任务页时调用）。
//
// 这是「懒生成」策略：不依赖定时任务，用户一访问就补齐当天实例。
// 好处是即使服务停机几天，恢复后用户一进来就能看到正确的实例。
func (s *TaskService) EnsureTodayInstances(ctx context.Context, uid uint) (int, error) {
	tasks, err := s.TaskRepo.ListActiveTasks(ctx, uid)
	if err != nil {
		return 0, apperrors.WithError(err)
	}

	// 先把过期的标为 expired
	if _, err := s.TaskRepo.ExpireOverdue(ctx, uid, s.Today()); err != nil {
		logger.ZWarn("expire overdue failed", zap.Uint("uid", uid), zap.Error(err))
	}

	created := 0
	for _, t := range tasks {
		periodKey := entities.PeriodKey(t.Cycle, s.Now())
		before, _, _ := s.TaskRepo.ListInstances(ctx, repository.InstanceQuery{
			UID: uid, TaskID: t.ID, PeriodKey: periodKey, Page: 1, PageSize: 1,
		})
		if len(before) > 0 {
			continue // 已存在，跳过（省一次写入）
		}
		if err := s.ensureInstanceForTask(ctx, uid, t); err != nil {
			logger.ZWarn("ensure instance failed",
				zap.Uint("uid", uid), zap.Uint("task_id", t.ID), zap.Error(err))
			continue
		}
		created++
	}
	return created, nil
}

// ============================ 计时 ============================

// StartTimer 开始计时
func (s *TaskService) StartTimer(ctx context.Context, uid, instID uint) (*entities.TaskInstance, error) {
	inst, err := s.mustGetInstance(ctx, uid, instID)
	if err != nil {
		return nil, err
	}
	if inst.Status != entities.StatusPending {
		return nil, apperrors.WithCode(apperrors.StatusNotAllowed)
	}
	if inst.StartedAt != nil {
		return inst, nil // 已在计时，幂等返回
	}

	now := time.Now().Unix()
	if err := s.TaskRepo.UpdateInstance(ctx, uid, instID, map[string]interface{}{
		"started_at": now,
		"paused_at":  nil,
	}); err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.TaskRepo.GetInstance(ctx, uid, instID)
}

// PauseTimer 暂停计时
func (s *TaskService) PauseTimer(ctx context.Context, uid, instID uint) (*entities.TaskInstance, error) {
	inst, err := s.mustGetInstance(ctx, uid, instID)
	if err != nil {
		return nil, err
	}
	if inst.StartedAt == nil {
		return nil, apperrors.WithCode(apperrors.StatusNotAllowed)
	}
	if inst.PausedAt != nil {
		return inst, nil // 已暂停，幂等
	}

	now := time.Now().Unix()
	if err := s.TaskRepo.UpdateInstance(ctx, uid, instID, map[string]interface{}{
		"paused_at": now,
	}); err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.TaskRepo.GetInstance(ctx, uid, instID)
}

// ResumeTimer 恢复计时
func (s *TaskService) ResumeTimer(ctx context.Context, uid, instID uint) (*entities.TaskInstance, error) {
	inst, err := s.mustGetInstance(ctx, uid, instID)
	if err != nil {
		return nil, err
	}
	if inst.StartedAt == nil || inst.PausedAt == nil {
		return nil, apperrors.WithCode(apperrors.StatusNotAllowed)
	}

	nowMs := time.Now().UnixMilli()
	pausedMs := nowMs - (*inst.PausedAt * 1000)
	newTotal := inst.PausedTotalMs + pausedMs

	if err := s.TaskRepo.UpdateInstance(ctx, uid, instID, map[string]interface{}{
		"paused_at":       nil,
		"paused_total_ms": newTotal,
	}); err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.TaskRepo.GetInstance(ctx, uid, instID)
}

// RunningMinutes 计算实例当前用时（分钟）
func (s *TaskService) RunningMinutes(inst *entities.TaskInstance) int {
	if inst.StartedAt == nil {
		return 0
	}
	return entities.RunningMinutes(*inst.StartedAt, inst.PausedAt, inst.PausedTotalMs, time.Now())
}

// ============================ 提交与审核 ============================

// Submit 提交任务（孩子点「做完了」）。
//
// 提交后进入 submitted 状态，等家长审核。
// 如果任务不需要审核（配置项），直接结算。
func (s *TaskService) Submit(ctx context.Context, uid, instID uint, actualMinutes int, backfilled bool) (*entities.TaskInstance, error) {
	inst, err := s.mustGetInstance(ctx, uid, instID)
	if err != nil {
		return nil, err
	}

	// 状态机校验
	if !inst.Status.CanTransitionTo(entities.StatusSubmitted) {
		return nil, apperrors.WithCode(apperrors.StatusNotAllowed)
	}

	// 实际用时：优先用传入值，否则按计时器算
	minutes := actualMinutes
	if minutes <= 0 {
		minutes = s.RunningMinutes(inst)
	}

	now := time.Now().Unix()
	rows, err := s.TaskRepo.UpdateInstanceStatusCAS(ctx, uid, instID,
		inst.Status, entities.StatusSubmitted,
		map[string]interface{}{
			"actual_minutes": minutes,
			"submitted_at":   now,
			"backfilled":     backfilled,
			"paused_at":      nil,
		})
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rows == 0 {
		// 并发：状态已被改
		return nil, apperrors.WithCode(apperrors.DuplicateOperation)
	}

	return s.TaskRepo.GetInstance(ctx, uid, instID)
}

// Review 家长审核（通过 / 打回）。
//
// approve=true：结算并发积分
// approve=false：回到 rejected 状态，孩子可以重做
func (s *TaskService) Review(ctx context.Context, uid, instID uint, approve bool, quality entities.QualityGrade, note string) (*entities.TaskInstance, error) {
	inst, err := s.mustGetInstance(ctx, uid, instID)
	if err != nil {
		return nil, err
	}
	if inst.Status != entities.StatusSubmitted {
		return nil, apperrors.WithCode(apperrors.StatusNotAllowed)
	}

	now := time.Now().Unix()

	// 打回
	if !approve {
		rows, err := s.TaskRepo.UpdateInstanceStatusCAS(ctx, uid, instID,
			entities.StatusSubmitted, entities.StatusRejected,
			map[string]interface{}{
				"review_note":   note,
				"reject_reason": note,
				"settled_at":    now,
			})
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		if rows == 0 {
			return nil, apperrors.WithCode(apperrors.DuplicateOperation)
		}
		return s.TaskRepo.GetInstance(ctx, uid, instID)
	}

	// 通过：结算
	if quality != "" && !quality.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	actual := 0
	if inst.ActualMinutes != nil {
		actual = *inst.ActualMinutes
	}

	result := entities.Settle(entities.SettlementInput{
		BasePoints:         inst.BasePoints,
		PlannedMinutes:     inst.PlannedMinutes,
		ActualMinutes:      actual,
		AllowOvertime:      inst.AllowOvertime,
		AllowLateNoPenalty: inst.AllowLateNoPenalty,
		QualityRated:       inst.QualityRated,
		Quality:            quality,
	})

	rows, err := s.TaskRepo.UpdateInstanceStatusCAS(ctx, uid, instID,
		entities.StatusSubmitted, result.Status,
		map[string]interface{}{
			"earned_points": result.Points,
			"quality":       string(quality),
			"settled_at":    now,
			"completed_at":  now,
			"review_note":   note,
		})
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rows == 0 {
		return nil, apperrors.WithCode(apperrors.DuplicateOperation)
	}

	// 发积分（幂等键防重复）
	if result.Points > 0 {
		_, _, err := s.WalletSrv.Change(ctx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyPoints,
			Delta:          int64(result.Points),
			Source:         entities.SourceTaskReward,
			RefID:          itoaUint(instID),
			Memo:           "完成任务：" + inst.Title,
			IdempotencyKey: "task_reward:" + itoaUint(instID),
		})
		if err != nil {
			logger.ZError("grant task reward failed",
				zap.Uint("uid", uid), zap.Uint("inst_id", instID), zap.Error(err))
			// 积分发放失败不影响任务状态（已有账本幂等键，可重试）
		}
	}

	// 发事件（异步，非关键路径）
	s.EventSrv.Publish(TaskCompletedEvent, map[string]interface{}{
		"uid":     uid,
		"task_id": inst.TaskID,
		"points":  result.Points,
	})

	return s.TaskRepo.GetInstance(ctx, uid, instID)
}

// Skip 跳过任务（主动放弃）
func (s *TaskService) Skip(ctx context.Context, uid, instID uint) (*entities.TaskInstance, error) {
	inst, err := s.mustGetInstance(ctx, uid, instID)
	if err != nil {
		return nil, err
	}
	if !inst.Status.CanTransitionTo(entities.StatusSkipped) {
		return nil, apperrors.WithCode(apperrors.StatusNotAllowed)
	}

	rows, err := s.TaskRepo.UpdateInstanceStatusCAS(ctx, uid, instID,
		inst.Status, entities.StatusSkipped,
		map[string]interface{}{"settled_at": time.Now().Unix()})
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rows == 0 {
		return nil, apperrors.WithCode(apperrors.DuplicateOperation)
	}
	return s.TaskRepo.GetInstance(ctx, uid, instID)
}

// ============================ 查询 ============================

// ListInstances 查询实例
func (s *TaskService) ListInstances(ctx context.Context, uid uint, q repository.InstanceQuery) ([]*entities.TaskInstance, int64, error) {
	q.UID = uid
	list, total, err := s.TaskRepo.ListInstances(ctx, q)
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}
	return list, total, nil
}

// TodayBoard 今日任务板（孩子端首页）
func (s *TaskService) TodayBoard(ctx context.Context, uid uint) (*entities.TaskBoardResp, error) {
	// 懒生成今天实例
	if _, err := s.EnsureTodayInstances(ctx, uid); err != nil {
		return nil, err
	}

	today := s.Today()
	list, _, err := s.TaskRepo.ListInstances(ctx, repository.InstanceQuery{
		UID: uid, Date: today, Page: 1, PageSize: 200,
	})
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	// 统计
	var (
		total, completed, pending, submitted int
		totalPoints                          int64
	)
	for _, inst := range list {
		total++
		switch inst.Status {
		case entities.StatusCompleted, entities.StatusFailed:
			completed++
		case entities.StatusSubmitted:
			submitted++
		default:
			pending++
		}
		if inst.EarnedPoints != nil {
			totalPoints += int64(*inst.EarnedPoints)
		}
	}

	// 钱包余额
	wallet, _ := s.WalletSrv.GetWallet(ctx, uid)
	var balance int64
	if wallet != nil {
		balance = wallet.Points
	}

	return &entities.TaskBoardResp{
		Date:        today,
		Total:       total,
		Completed:   completed,
		Pending:     pending,
		Submitted:   submitted,
		TodayPoints: totalPoints,
		Balance:     balance,
		Instances:   list,
	}, nil
}

// PendingReview 待审核列表（家长端）
func (s *TaskService) PendingReview(ctx context.Context, uid uint, page, pageSize int) ([]*entities.TaskInstance, int64, error) {
	return s.ListInstances(ctx, uid, repository.InstanceQuery{
		UID:      uid,
		Statuses: []entities.TaskStatus{entities.StatusSubmitted},
		Page:     page,
		PageSize: pageSize,
	})
}

// Stats 任务统计（家长看板）
func (s *TaskService) Stats(ctx context.Context, uid uint, dateFrom, dateTo string) (map[string]interface{}, error) {
	if dateFrom == "" {
		dateFrom = entities.DateString(s.Now().AddDate(0, 0, -30))
	}
	if dateTo == "" {
		dateTo = s.Today()
	}

	list, total, err := s.TaskRepo.ListInstances(ctx, repository.InstanceQuery{
		UID: uid, DateFrom: dateFrom, DateTo: dateTo, Page: 1, PageSize: 1000,
	})
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	byStatus := make(map[string]int, 7)
	var points int64
	for _, inst := range list {
		byStatus[string(inst.Status)]++
		if inst.EarnedPoints != nil {
			points += int64(*inst.EarnedPoints)
		}
	}

	sumPoints, _ := s.TaskRepo.SumEarnedPoints(ctx, uid, dateFrom, dateTo)

	return map[string]interface{}{
		"date_from":     dateFrom,
		"date_to":       dateTo,
		"total":         total,
		"by_status":     byStatus,
		"earned_points": points,
		"ledger_points": sumPoints,
	}, nil
}

// ============================ 辅助 ============================

func (s *TaskService) mustGetInstance(ctx context.Context, uid, instID uint) (*entities.TaskInstance, error) {
	inst, err := s.TaskRepo.GetInstance(ctx, uid, instID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if inst == nil {
		return nil, apperrors.WithCode(apperrors.TaskInstanceNotExist)
	}
	return inst, nil
}

// itoaUint uint 转字符串
func itoaUint(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
