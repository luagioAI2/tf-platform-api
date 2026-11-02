package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"
	"tf-api/internal/app/service/repository"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var TaskAPISet = wire.NewSet(wire.Struct(new(TaskAPI), "*"))

// TaskAPI 任务接口
type TaskAPI struct {
	Srv      *service.TaskService
	CheckSrv *service.CheckInService
}

// ============================ 任务定义 ============================

// CreateTask 创建任务
// @Tags Task[任务]
// @Summary 创建任务
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.CreateTaskReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.Task}
// @Router /api/task/create [post]
func (c *TaskAPI) CreateTask(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.CreateTaskReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	task, err := c.Srv.CreateTask(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, task)
}

// UpdateTask 更新任务
// @Tags Task[任务]
// @Summary 更新任务
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.UpdateTaskReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.Task}
// @Router /api/task/update [post]
func (c *TaskAPI) UpdateTask(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		TaskID uint `json:"task_id" binding:"required,min=1"`
		entities.UpdateTaskReq
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	task, err := c.Srv.UpdateTask(ctx.Request.Context(), uid, req.TaskID, &req.UpdateTaskReq)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, task)
}

// DeleteTask 删除任务
// @Tags Task[任务]
// @Summary 删除任务
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/task/delete [post]
func (c *TaskAPI) DeleteTask(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	// 删除的是**任务定义**，所以参数是 task_id。
	// 2026-11-02 之前这里错用了 entities.TimerReq（字段名 instance_id），
	// 传 task_id 会报「instance_id为必填字段」，传 instance_id 又会去删同号的任务
	// —— 一个从没被客户端调过的接口，两种写法都是错的。
	var req struct {
		TaskID uint `json:"task_id" binding:"required,min=1"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.DeleteTask(ctx.Request.Context(), uid, req.TaskID); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"deleted": true})
}

// ListTasks 任务列表
// @Tags Task[任务]
// @Summary 任务定义列表
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/task/list [post]
func (c *TaskAPI) ListTasks(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		IncludeArchived bool `json:"include_archived"`
	}
	_ = ctx.ShouldBindJSON(&req)

	list, err := c.Srv.ListTasks(ctx.Request.Context(), uid, req.IncludeArchived)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": list, "count": len(list)})
}

// ============================ 任务板 ============================

// Board 今日任务板
// @Tags Task[任务]
// @Summary 今日任务板（孩子端首页）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.TaskBoardResp}
// @Router /api/task/board [post]
func (c *TaskAPI) Board(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	board, err := c.Srv.TodayBoard(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, board)
}

// Instances 查询任务实例
// @Tags Task[任务]
// @Summary 查询任务实例列表
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ListTaskInstanceReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/task/instances [post]
func (c *TaskAPI) Instances(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ListTaskInstanceReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	statuses := make([]entities.TaskStatus, 0, len(req.Status))
	for _, s := range req.Status {
		statuses = append(statuses, entities.TaskStatus(s))
	}

	list, total, err := c.Srv.ListInstances(ctx.Request.Context(), uid, repositoryQuery(uid, req, statuses))
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"page": req.Page, "pageSize": req.PageSize,
		"count": total, "list": list,
	})
}

// ============================ 计时 ============================

// StartTimer 开始计时
// @Tags Task[任务]
// @Summary 开始计时
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.TimerReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TaskInstance}
// @Router /api/task/start-timer [post]
func (c *TaskAPI) StartTimer(ctx *gin.Context) {
	c.timerOp(ctx, "start")
}

// PauseTimer 暂停计时
// @Tags Task[任务]
// @Summary 暂停计时
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.TimerReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TaskInstance}
// @Router /api/task/pause-timer [post]
func (c *TaskAPI) PauseTimer(ctx *gin.Context) {
	c.timerOp(ctx, "pause")
}

// ResumeTimer 恢复计时
// @Tags Task[任务]
// @Summary 恢复计时
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.TimerReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TaskInstance}
// @Router /api/task/resume-timer [post]
func (c *TaskAPI) ResumeTimer(ctx *gin.Context) {
	c.timerOp(ctx, "resume")
}

func (c *TaskAPI) timerOp(ctx *gin.Context, op string) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.TimerReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	var (
		inst *entities.TaskInstance
		err  error
	)
	switch op {
	case "start":
		inst, err = c.Srv.StartTimer(ctx.Request.Context(), uid, req.InstanceID)
	case "pause":
		inst, err = c.Srv.PauseTimer(ctx.Request.Context(), uid, req.InstanceID)
	case "resume":
		inst, err = c.Srv.ResumeTimer(ctx.Request.Context(), uid, req.InstanceID)
	}
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, inst)
}

// ============================ 提交与审核 ============================

// Submit 提交任务
// @Tags Task[任务]
// @Summary 提交任务（孩子点"做完了"）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.SubmitTaskReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TaskInstance}
// @Router /api/task/submit [post]
func (c *TaskAPI) Submit(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.SubmitTaskReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	inst, err := c.Srv.Submit(ctx.Request.Context(), uid, req.InstanceID, req.ActualMinutes, req.Backfilled)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, inst)
}

// Review 审核任务
// @Tags Task[任务]
// @Summary 家长审核（通过/打回）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ReviewTaskReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TaskInstance}
// @Router /api/task/review [post]
func (c *TaskAPI) Review(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ReviewTaskReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	inst, err := c.Srv.Review(ctx.Request.Context(), uid, req.InstanceID, req.Approve, req.Quality, req.Note)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, inst)
}

// Skip 跳过任务
// @Tags Task[任务]
// @Summary 跳过任务
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.TimerReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TaskInstance}
// @Router /api/task/skip [post]
func (c *TaskAPI) Skip(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.TimerReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	inst, err := c.Srv.Skip(ctx.Request.Context(), uid, req.InstanceID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, inst)
}

// PendingReview 待审核列表
// @Tags Task[任务]
// @Summary 待审核列表（家长端）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/task/pending-review [post]
func (c *TaskAPI) PendingReview(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	_ = ctx.ShouldBindJSON(&req)

	list, total, err := c.Srv.PendingReview(ctx.Request.Context(), uid, req.Page, req.PageSize)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"count": total, "list": list})
}

// Stats 任务统计
// @Tags Task[任务]
// @Summary 任务统计（家长看板）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/task/stats [post]
func (c *TaskAPI) Stats(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	_ = ctx.ShouldBindJSON(&req)

	stats, err := c.Srv.Stats(ctx.Request.Context(), uid, req.DateFrom, req.DateTo)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, stats)
}

// ============================ 签到 ============================

// CheckIn 签到
// @Tags Task[任务]
// @Summary 签到
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.CheckInReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.CheckInResp}
// @Router /api/task/check-in [post]
func (c *TaskAPI) CheckIn(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.CheckInReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.CheckSrv.CheckIn(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// CheckInStatus 签到状态
// @Tags Task[任务]
// @Summary 签到状态
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.CheckInReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.CheckInStatusResp}
// @Router /api/task/check-in-status [post]
func (c *TaskAPI) CheckInStatus(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.CheckInReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.CheckSrv.Status(ctx.Request.Context(), uid, req.TaskID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// ============================ 辅助 ============================

// repositoryQuery 构造 Repository 查询条件
func repositoryQuery(uid uint, req entities.ListTaskInstanceReq, statuses []entities.TaskStatus) repository.InstanceQuery {
	return repository.InstanceQuery{
		UID:       uid,
		TaskID:    req.TaskID,
		PeriodKey: req.PeriodKey,
		Date:      req.Date,
		DateFrom:  req.DateFrom,
		DateTo:    req.DateTo,
		Statuses:  statuses,
		Page:      req.Page,
		PageSize:  req.PageSize,
	}
}
