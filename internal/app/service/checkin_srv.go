package service

import (
	"context"
	"fmt"
	"time"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var CheckInServiceSet = wire.NewSet(ProvideCheckInService)

// 签到阶梯奖励默认倍率（第 N 天给 N × 基础分的这个比例）
const DefaultTierRatio = 0.8

// CheckInService 签到服务
type CheckInService struct {
	TaskRepo  *repository.TaskRepository
	CheckRepo *repository.CheckInRepository
	WalletSrv *WalletService
	EventSrv  *EventService
	timezone  *time.Location
}

// ProvideCheckInService 构造函数
func ProvideCheckInService(
	taskRepo *repository.TaskRepository,
	checkRepo *repository.CheckInRepository,
	walletSrv *WalletService,
	eventSrv *EventService,
) *CheckInService {
	return &CheckInService{
		TaskRepo:  taskRepo,
		CheckRepo: checkRepo,
		WalletSrv: walletSrv,
		EventSrv:  eventSrv,
		timezone:  time.Local,
	}
}

func (s *CheckInService) now() time.Time { return time.Now().In(s.timezone) }

// CheckIn 签到。
//
// 流程：
//  1. 校验任务是签到任务
//  2. 校验今天未签到
//  3. 写签到记录（唯一索引兜底防重复）
//  4. 发积分（阶梯奖励）
//  5. 更新进度
func (s *CheckInService) CheckIn(ctx context.Context, uid uint, req *entities.CheckInReq) (*entities.CheckInResp, error) {
	task, err := s.TaskRepo.GetTask(ctx, uid, req.TaskID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if task == nil {
		return nil, apperrors.WithCode(apperrors.TaskNotExist)
	}
	if !task.CheckInEnabled {
		return nil, apperrors.WithCode(apperrors.TaskNotCheckInTask)
	}

	now := s.now()
	date := entities.DateString(now)
	periodKey := entities.PeriodKey(task.Cycle, now)

	// 今天是否已签到（含待审核）
	progress, err := s.CheckRepo.GetProgress(ctx, uid, task.ID, periodKey)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if progress != nil {
		for _, d := range progress.Days {
			if d == date {
				return nil, apperrors.WithCode(apperrors.TaskAlreadyCheckedIn)
			}
		}
		for _, d := range progress.PendingDays {
			if d == date {
				return nil, apperrors.WithCode(apperrors.TaskAlreadyCheckedIn)
			}
		}
	}

	// 写签到记录（唯一索引防并发重复）
	rec := &entities.CheckInRecord{
		UID:       uid,
		TaskID:    task.ID,
		Date:      date,
		PeriodKey: periodKey,
		Points:    0,
	}
	if err := s.CheckRepo.CreateRecord(ctx, rec); err != nil {
		return nil, apperrors.WithError(err)
	}

	// 更新进度
	if progress == nil {
		progress = &entities.CheckInProgress{
			UID:          uid,
			TaskID:       task.ID,
			PeriodKey:    periodKey,
			Days:         entities.JSONStringArray{},
			PendingDays:  entities.JSONStringArray{},
			ClaimedTiers: entities.JSONIntArray{},
		}
	}
	progress.Days = append(progress.Days, date)

	// 计算连续天数
	continuous := s.continuousDays(progress.Days, now)

	// 阶梯奖励：达到目标次数给额外奖励
	var claimed []int
	basePoints := task.BasePoints
	target := task.CheckInTargetCount
	if target <= 0 {
		target = 7 // 默认 7 天
	}

	points := basePoints
	// 每完成 target 天，给一次阶梯奖励
	tier := len(progress.Days) / target * target
	if tier > 0 && !containsInt(progress.ClaimedTiers, tier) {
		bonus := int(float64(basePoints) * DefaultTierRatio * float64(target))
		points += bonus
		progress.ClaimedTiers = append(progress.ClaimedTiers, tier)
		claimed = append(claimed, tier)
	}

	if err := s.CheckRepo.UpsertProgress(ctx, progress); err != nil {
		return nil, apperrors.WithError(err)
	}

	// 发积分
	if points > 0 {
		_, _, err := s.WalletSrv.Change(ctx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyPoints,
			Delta:          int64(points),
			Source:         entities.SourceCheckIn,
			RefID:          fmt.Sprintf("%d:%s", task.ID, date),
			Memo:           "签到：" + task.Title,
			IdempotencyKey: fmt.Sprintf("checkin:%d:%s", task.ID, date),
		})
		if err != nil {
			logger.ZError("grant checkin points failed",
				zap.Uint("uid", uid), zap.Uint("task_id", task.ID), zap.Error(err))
		}
	}

	s.EventSrv.Publish(TaskCheckedInEvent, map[string]interface{}{
		"uid":     uid,
		"task_id": task.ID,
		"date":    date,
		"points":  points,
	})

	return &entities.CheckInResp{
		TaskID:         task.ID,
		Date:           date,
		ContinuousDays: continuous,
		Points:         points,
		NeedReview:     false,
		ClaimedTiers:   claimed,
	}, nil
}

// Status 查询签到状态
func (s *CheckInService) Status(ctx context.Context, uid, taskID uint) (*entities.CheckInStatusResp, error) {
	task, err := s.TaskRepo.GetTask(ctx, uid, taskID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if task == nil {
		return nil, apperrors.WithCode(apperrors.TaskNotExist)
	}
	if !task.CheckInEnabled {
		return nil, apperrors.WithCode(apperrors.TaskNotCheckInTask)
	}

	now := s.now()
	date := entities.DateString(now)
	periodKey := entities.PeriodKey(task.Cycle, now)

	progress, err := s.CheckRepo.GetProgress(ctx, uid, taskID, periodKey)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	resp := &entities.CheckInStatusResp{
		TaskID:      taskID,
		Title:       task.Title,
		PeriodKey:   periodKey,
		TargetCount: task.CheckInTargetCount,
	}
	if progress != nil {
		resp.CheckedDays = []string(progress.Days)
		resp.PendingDays = []string(progress.PendingDays)
		resp.ClaimedTiers = []int(progress.ClaimedTiers)
		resp.ContinuousDays = s.continuousDays(progress.Days, now)
		for _, d := range progress.Days {
			if d == date {
				resp.TodayChecked = true
			}
		}
		for _, d := range progress.PendingDays {
			if d == date {
				resp.TodayChecked = true
			}
		}
	}
	return resp, nil
}

// continuousDays 计算连续签到天数（从今天或昨天往前数）
func (s *CheckInService) continuousDays(days []string, now time.Time) int {
	if len(days) == 0 {
		return 0
	}

	set := make(map[string]struct{}, len(days))
	for _, d := range days {
		set[d] = struct{}{}
	}

	// 从今天开始往前数；如果今天没签，从昨天开始
	cur := now
	if _, ok := set[entities.DateString(cur)]; !ok {
		cur = cur.AddDate(0, 0, -1)
		if _, ok := set[entities.DateString(cur)]; !ok {
			return 0
		}
	}

	count := 0
	for {
		if _, ok := set[entities.DateString(cur)]; !ok {
			break
		}
		count++
		cur = cur.AddDate(0, 0, -1)
	}
	return count
}

// containsInt 判断切片是否包含某值
func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
