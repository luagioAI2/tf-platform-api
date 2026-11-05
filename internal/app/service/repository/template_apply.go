package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"tf-api/internal/app/entities"

	"gorm.io/gorm"
)

// ============================ 模板 → 实例 ============================
//
// 模板条目（`template_item`）是**后台的表**：typed 列是 snake_case
// （`base_points` / `limit_per_day`），Extra 里是 **App 原生的 camelCase**
// （`allowOvertime` / `checkInEnabled` / `rewardItemIds` …）。
//
// 铺实例时要把它拆回服务端的 `task` / `redeem_item` 行：
//   · 有列的 → 写到列上（否则 App 读不到：它读的是 `check_in_enabled`，
//     不是 Extra 里的 `checkInEnabled`）；
//   · 没列的 → 原样留给 `Task.Extra`（App 的 `parseTaskExtra` 会捡走
//     `checkInTierRatio` 这类玩法参数）。
//
// ★ 漏拆一个的症状是**静默的**：签到任务变成普通任务、奖励道具不掉、
//   提醒时间消失 —— 都不报错，只有孩子发现「签到按钮不见了」。

// ApplyPlan 本次要落地的模板（service 解析好，repo 只负责写）。
type ApplyPlan struct {
	TaskTemplate   *entities.Template
	TaskItems      []entities.TemplateItem
	RedeemTemplate *entities.Template
	RedeemItems    []entities.TemplateItem
}

// ErrApplyNoUser uid=0 时拒绝铺 —— 那会把模板内容写成「无主任务」，
// 谁也看不见，而且永远清理不掉。
var ErrApplyNoUser = errors.New("apply template: empty uid")

// AppliedOf 读「这个账号铺过没有」。没有返回 (nil, nil)。
func (r *TemplateRepository) AppliedOf(ctx context.Context, uid uint) (*entities.TemplateApply, error) {
	var row entities.TemplateApply
	err := r.DB.WithContext(ctx).Where("uid = ?", uid).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ApplyOnce 给账号铺一次初始任务 / 兑换清单。
//
// 返回 (nil, nil) = 这个账号以前铺过（不是错误，调用方按 `already` 回话）。
//
// 判据是**每一类各自的表空不空**：任务表非空就不动任务，但兑换表为空时
// 仍然补一份 —— 老账号（兑换商城是后加的）正好是这种情况。
func (r *TemplateRepository) ApplyOnce(ctx context.Context, uid uint, plan ApplyPlan) (*entities.TemplateApply, error) {
	if uid == 0 {
		return nil, ErrApplyNoUser
	}

	// 先读一次：绝大多数启动走到这里就返回了，不必开事务。
	if row, err := r.AppliedOf(ctx, uid); err != nil {
		return nil, err
	} else if row != nil {
		return nil, nil
	}

	row := &entities.TemplateApply{UID: uid}
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		/* ① 抢锁：唯一键 uid。并发时第二个 INSERT 直接冲突 ⇒ 当「已经铺过」。 */
		if err := tx.Create(row).Error; err != nil {
			if isDuplicateKey(err) {
				row = nil
				return nil
			}
			return err
		}

		var taskCount, redeemCount int64
		if err := tx.Model(&entities.Task{}).Where("uid = ?", uid).Count(&taskCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&entities.RedeemItem{}).Where("uid = ?", uid).Count(&redeemCount).Error; err != nil {
			return err
		}

		if taskCount == 0 && plan.TaskTemplate != nil {
			for _, it := range plan.TaskItems {
				if err := tx.Create(TaskFromTemplateItem(uid, it)).Error; err != nil {
					return err
				}
				row.TaskCount++
			}
			row.TaskTemplateID = plan.TaskTemplate.ID
		}

		if redeemCount == 0 && plan.RedeemTemplate != nil {
			/* `item_id` 在 (uid, item_id) 上唯一：同一份模板里若有人填了
			   重复的业务 ID，第二次插入会失败 ⇒ **整笔事务回滚**，
			   用户一条都拿不到。这里先按 id 去重，宁可少一条也不要整笔失败。 */
			seen := make(map[string]bool, len(plan.RedeemItems))
			for _, it := range plan.RedeemItems {
				itemID := strings.TrimSpace(it.ItemID)
				if itemID == "" || seen[itemID] {
					continue
				}
				seen[itemID] = true
				if err := tx.Create(RedeemItemFromTemplateItem(uid, it)).Error; err != nil {
					return err
				}
				row.RedeemCount++
			}
			row.RedeemTemplateID = plan.RedeemTemplate.ID
		}

		if taskCount > 0 || redeemCount > 0 {
			row.Skipped = entities.ApplyReasonHasContent
		}

		/* ② 把「用了哪份模板 / 铺了几条」写回去。创建时这些还是零值，
		      所以必须再来一次 UPDATE。 */
		return tx.Model(&entities.TemplateApply{}).Where("id = ?", row.ID).
			Updates(map[string]interface{}{
				"task_template_id":   row.TaskTemplateID,
				"redeem_template_id": row.RedeemTemplateID,
				"task_count":         row.TaskCount,
				"redeem_count":       row.RedeemCount,
				"skipped":            row.Skipped,
				"updated_at":         time.Now().Unix(),
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// isDuplicateKey 唯一键冲突（SQLite / MySQL 两种措辞）。
//
// 不用 `errors.Is(err, gorm.ErrDuplicatedKey)`：那要求 `gorm.Config`
// 打开 `TranslateError`，本项目没开 —— 写了也是永远 false，
// 于是「已经铺过」会被当成真错误，客户端看到 500。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") || // SQLite
		strings.Contains(msg, "duplicate entry") // MySQL
}

// ============================ 纯函数：条目 → 实例 ============================

// TaskFromTemplateItem 把模板条目变成一条属于该账号的任务。
func TaskFromTemplateItem(uid uint, it entities.TemplateItem) *entities.Task {
	ex := decodeExtra(it.Extra)

	cycle := entities.TaskCycle(it.Cycle)
	if !cycle.IsValid() {
		// 后台表单理论上拦得住，但模板是可以被脚本 / 旧版本写进来的。
		// 周期非法会让 `PeriodKey` 算出空串 ⇒ 实例永远不生成 ⇒ 任务看得见点不动。
		cycle = entities.CycleDaily
	}
	category := entities.TaskCategory(it.Category)
	if !category.IsValid() {
		category = entities.CategoryStudy
	}

	planned := it.PlannedMinutes
	if planned <= 0 {
		planned = 30
	}
	points := it.BasePoints
	if points <= 0 {
		points = 10
	}
	target := intOf(ex, "checkInTargetCount")
	if boolOf(ex, "checkInEnabled") && target <= 0 {
		target = 1
	}

	t := &entities.Task{
		UID:                uid,
		Title:              it.Name,
		Note:               it.Note,
		Category:           category,
		Cycle:              cycle,
		PlannedMinutes:     planned,
		BasePoints:         points,
		AllowOvertime:      boolOf(ex, "allowOvertime"),
		AllowLateNoPenalty: boolOf(ex, "allowLateNoPenalty"),
		QualityRated:       boolOf(ex, "qualityRated"),
		CheckInEnabled:     boolOf(ex, "checkInEnabled"),
		CheckInTargetCount: target,
		RemindAt:           strOf(ex, "remindAt"),
		RewardItemIDs:      strSliceOf(ex, "rewardItemIds"),
		Sort:               it.Sort,
	}
	if v, ok := ex["windowStartMinute"]; ok {
		if n, ok := toInt(v); ok {
			t.WindowStartMinute = &n
		}
	}
	if v, ok := ex["windowEndMinute"]; ok {
		if n, ok := toInt(v); ok {
			t.WindowEndMinute = &n
		}
	}

	// 已经落到列上的键从 Extra 里剔掉，剩下的（`checkInTierRatio` 之类）
	// 原样留给客户端 —— 剔干净是为了不让「列」和「Extra」各存一份而互相打架。
	delete(ex, "allowOvertime")
	delete(ex, "allowLateNoPenalty")
	delete(ex, "qualityRated")
	delete(ex, "checkInEnabled")
	delete(ex, "checkInTargetCount")
	delete(ex, "remindAt")
	delete(ex, "rewardItemIds")
	delete(ex, "windowStartMinute")
	delete(ex, "windowEndMinute")
	t.Extra = encodeExtra(ex)

	return t
}

// RedeemItemFromTemplateItem 把模板条目变成一件属于该账号的兑换品。
//
// ★ `ItemID` 用模板条目的业务 ID：同一份模板铺到不同账号时 id 相同，
//   但唯一索引是 (uid, item_id)，所以互不冲突；重复铺同一个人也能靠它去重。
func RedeemItemFromTemplateItem(uid uint, it entities.TemplateItem) *entities.RedeemItem {
	cat := entities.RedeemCategory(it.Category)
	if !cat.IsValid() {
		cat = entities.RedeemCategoryCustom
	}
	stock := it.Stock
	if stock == 0 {
		// 模板里 `stock` 的零值有两种来源：后台没填（想表达无限）和
		// 真的填了 0。前者是绝大多数，所以按「无限」处理 ——
		// 反过来会把整份默认清单变成「一件都换不了」，而且不报错。
		stock = -1
	}
	price := it.Price
	if price < 0 {
		price = 0
	}
	return &entities.RedeemItem{
		UID:             uid,
		ItemID:          strings.TrimSpace(it.ItemID),
		Name:            it.Name,
		Emoji:           it.Emoji,
		Category:        cat,
		Price:           price,
		Note:            it.Note,
		LimitPerDay:     it.LimitPerDay,
		Once:            it.Once,
		Stock:           stock,
		CreatedByParent: true,
		Sort:            it.Sort,
	}
}

// ---------------- Extra 取值小工具 ----------------

func decodeExtra(raw string) map[string]interface{} {
	out := map[string]interface{}{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Extra 坏了就当没有：宁可少几个玩法参数，也不要整笔落地失败
		return map[string]interface{}{}
	}
	return out
}

func encodeExtra(m map[string]interface{}) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

func boolOf(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case float64:
		return x != 0
	}
	return false
}

func intOf(m map[string]interface{}, key string) int {
	if v, ok := toInt(m[key]); ok {
		return v
	}
	return 0
}

func toInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	}
	return 0, false
}

func strOf(m map[string]interface{}, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func strSliceOf(m map[string]interface{}, key string) entities.JSONStringArray {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make(entities.JSONStringArray, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
