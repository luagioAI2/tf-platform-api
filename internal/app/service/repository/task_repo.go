package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var TaskRepositorySet = wire.NewSet(wire.Struct(new(TaskRepository), "*"))

// TaskRepository 任务定义与实例数据访问
type TaskRepository struct {
	DB *gorm.DB
}

// NewTaskRepository 直接构造（供脚本使用）
func NewTaskRepository(db *gorm.DB) *TaskRepository {
	return &TaskRepository{DB: db}
}

// ============================ 任务定义 ============================

// CreateTask 创建任务
func (r *TaskRepository) CreateTask(ctx context.Context, t *entities.Task) error {
	return r.DB.WithContext(ctx).Create(t).Error
}

// GetTask 按 ID 查询任务
func (r *TaskRepository) GetTask(ctx context.Context, uid, taskID uint) (*entities.Task, error) {
	var t entities.Task
	err := r.DB.WithContext(ctx).
		Where("id = ? AND uid = ?", taskID, uid).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks 列出用户的任务（可按归档筛选）
func (r *TaskRepository) ListTasks(ctx context.Context, uid uint, includeArchived bool) ([]*entities.Task, error) {
	tx := r.DB.WithContext(ctx).Where("uid = ?", uid)
	if !includeArchived {
		tx = tx.Where("archived = ?", false)
	}
	var list []*entities.Task
	err := tx.Order("sort DESC, id ASC").Find(&list).Error
	return list, err
}

// ListActiveTasks 列出所有未归档任务（用于生成实例）
func (r *TaskRepository) ListActiveTasks(ctx context.Context, uid uint) ([]*entities.Task, error) {
	var list []*entities.Task
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND archived = ?", uid, false).
		Find(&list).Error
	return list, err
}

// UpdateTask 更新任务（只更新非空字段）
func (r *TaskRepository) UpdateTask(ctx context.Context, uid, taskID uint, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&entities.Task{}).
		Where("id = ? AND uid = ?", taskID, uid).
		Updates(fields).Error
}

// DeleteTask 删除任务（软删除）
func (r *TaskRepository) DeleteTask(ctx context.Context, uid, taskID uint) error {
	return r.DB.WithContext(ctx).
		Where("id = ? AND uid = ?", taskID, uid).
		Delete(&entities.Task{}).Error
}

// ============================ 任务实例 ============================

// CreateInstance 创建实例（唯一索引冲突时忽略，返回已有记录）
func (r *TaskRepository) CreateInstance(ctx context.Context, inst *entities.TaskInstance) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "uid"}, {Name: "task_id"}, {Name: "period_key"}, {Name: "date"},
		},
		DoNothing: true,
	}).Create(inst).Error
}

// GetInstance 按 ID 查询实例
func (r *TaskRepository) GetInstance(ctx context.Context, uid, instID uint) (*entities.TaskInstance, error) {
	var inst entities.TaskInstance
	err := r.DB.WithContext(ctx).
		Where("id = ? AND uid = ?", instID, uid).
		First(&inst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

// GetInstanceByKey 按 (task, period, date) 查询
func (r *TaskRepository) GetInstanceByKey(ctx context.Context, uid, taskID uint, periodKey, date string) (*entities.TaskInstance, error) {
	var inst entities.TaskInstance
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND task_id = ? AND period_key = ? AND date = ?",
			uid, taskID, periodKey, date).
		First(&inst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

// ListInstances 按条件查询实例
func (r *TaskRepository) ListInstances(ctx context.Context, q InstanceQuery) ([]*entities.TaskInstance, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.TaskInstance{}).Where("uid = ?", q.UID)

	if q.TaskID > 0 {
		tx = tx.Where("task_id = ?", q.TaskID)
	}
	if q.PeriodKey != "" {
		tx = tx.Where("period_key = ?", q.PeriodKey)
	}
	if q.Date != "" {
		tx = tx.Where("date = ?", q.Date)
	}
	if q.DateFrom != "" {
		tx = tx.Where("date >= ?", q.DateFrom)
	}
	if q.DateTo != "" {
		tx = tx.Where("date <= ?", q.DateTo)
	}
	if len(q.Statuses) > 0 {
		tx = tx.Where("status IN ?", q.Statuses)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, pageSize := q.Page, q.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 30
	}

	var list []*entities.TaskInstance
	err := tx.Order("date DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&list).Error
	return list, total, err
}

// InstanceQuery 实例查询条件
type InstanceQuery struct {
	UID       uint
	TaskID    uint
	PeriodKey string
	Date      string
	DateFrom  string
	DateTo    string
	Statuses  []entities.TaskStatus
	Page      int
	PageSize  int
}

// UpdateInstance 更新实例（带状态机校验由 Service 层负责）
func (r *TaskRepository) UpdateInstance(ctx context.Context, uid, instID uint, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&entities.TaskInstance{}).
		Where("id = ? AND uid = ?", instID, uid).
		Updates(fields).Error
}

// UpdateInstanceStatusCAS 带状态校验的更新（CAS，防止并发重复结算）
//
// 返回 RowsAffected；为 0 表示状态已被其他请求改过。
func (r *TaskRepository) UpdateInstanceStatusCAS(
	ctx context.Context, uid, instID uint, from, to entities.TaskStatus, extra map[string]interface{},
) (int64, error) {
	fields := map[string]interface{}{"status": string(to)}
	for k, v := range extra {
		fields[k] = v
	}
	res := r.DB.WithContext(ctx).Model(&entities.TaskInstance{}).
		Where("id = ? AND uid = ? AND status = ?", instID, uid, string(from)).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// CountInstancesByStatus 统计某状态的实例数（家长端红点）
func (r *TaskRepository) CountInstancesByStatus(ctx context.Context, uid uint, status entities.TaskStatus) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.TaskInstance{}).
		Where("uid = ? AND status = ?", uid, string(status)).
		Count(&count).Error
	return count, err
}

// SumEarnedPoints 统计某时间段获得的积分（家长看板）
func (r *TaskRepository) SumEarnedPoints(ctx context.Context, uid uint, dateFrom, dateTo string) (int64, error) {
	var sum int64
	err := r.DB.WithContext(ctx).Model(&entities.TaskInstance{}).
		Where("uid = ? AND date >= ? AND date <= ? AND earned_points IS NOT NULL", uid, dateFrom, dateTo).
		Select("COALESCE(SUM(earned_points), 0)").Scan(&sum).Error
	return sum, err
}

// ExpireOverdue 把过期的待完成实例标记为 expired
//
// 只处理 date < today 且 status = pending 的实例。
func (r *TaskRepository) ExpireOverdue(ctx context.Context, uid uint, today string) (int64, error) {
	res := r.DB.WithContext(ctx).Model(&entities.TaskInstance{}).
		Where("uid = ? AND date < ? AND status = ?", uid, today, string(entities.StatusPending)).
		Update("status", string(entities.StatusExpired))
	return res.RowsAffected, res.Error
}

// ============================ 签到 ============================

var CheckInRepositorySet = wire.NewSet(wire.Struct(new(CheckInRepository), "*"))

// CheckInRepository 签到数据访问
type CheckInRepository struct {
	DB *gorm.DB
}

// NewCheckInRepository 直接构造
func NewCheckInRepository(db *gorm.DB) *CheckInRepository {
	return &CheckInRepository{DB: db}
}

// CreateRecord 创建签到记录（唯一索引冲突忽略）
func (r *CheckInRepository) CreateRecord(ctx context.Context, rec *entities.CheckInRecord) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uid"}, {Name: "task_id"}, {Name: "date"}},
		DoNothing: true,
	}).Create(rec).Error
}

// ListRecords 查询签到记录
func (r *CheckInRepository) ListRecords(ctx context.Context, uid, taskID uint, periodKey string) ([]*entities.CheckInRecord, error) {
	tx := r.DB.WithContext(ctx).Where("uid = ?", uid)
	if taskID > 0 {
		tx = tx.Where("task_id = ?", taskID)
	}
	if periodKey != "" {
		tx = tx.Where("period_key = ?", periodKey)
	}
	var list []*entities.CheckInRecord
	err := tx.Order("date ASC").Find(&list).Error
	return list, err
}

// GetProgress 查询签到进度
func (r *CheckInRepository) GetProgress(ctx context.Context, uid, taskID uint, periodKey string) (*entities.CheckInProgress, error) {
	var p entities.CheckInProgress
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND task_id = ? AND period_key = ?", uid, taskID, periodKey).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertProgress 写入签到进度
func (r *CheckInRepository) UpsertProgress(ctx context.Context, p *entities.CheckInProgress) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "uid"}, {Name: "task_id"}, {Name: "period_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"days", "pending_days", "claimed_tiers", "updated_at",
		}),
	}).Create(p).Error
}

// HasCheckedIn 今天是否已签到
func (r *CheckInRepository) HasCheckedIn(ctx context.Context, uid, taskID uint, date string) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.CheckInRecord{}).
		Where("uid = ? AND task_id = ? AND date = ?", uid, taskID, date).
		Count(&count).Error
	return count > 0, err
}
