package repository

import (
	"encoding/json"
	"testing"

	"tf-api/internal/app/entities"
)

// 模板条目 → 任务：Extra 里的 camelCase 玩法参数必须**落到列上**。
//
// 这是整条链最容易被判「没问题」的一段：铺出来的任务标题、积分都对，
// 但签到开关没拆过来 ⇒ 签到任务变成普通任务。界面不报错，
// 只有孩子发现「签到按钮不见了」。所以逐键断言。
func TestTaskFromTemplateItemSplitsExtra(t *testing.T) {
	it := entities.TemplateItem{
		ItemID:         "official-task-01",
		Name:           "完成语文作业",
		Note:           "认真写，字要工整哦",
		Category:       "study",
		Cycle:          "daily",
		PlannedMinutes: 45,
		BasePoints:     10,
		Sort:           3,
		Extra:          `{"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"remindAt":"18:30"}`,
	}
	task := TaskFromTemplateItem(7, it)

	if task.UID != 7 || task.Title != "完成语文作业" || task.Note != "认真写，字要工整哦" {
		t.Fatalf("基础字段没搬过去: %+v", task)
	}
	if task.Category != entities.CategoryStudy || task.Cycle != entities.CycleDaily {
		t.Fatalf("分类/周期错: %s / %s", task.Category, task.Cycle)
	}
	if task.PlannedMinutes != 45 || task.BasePoints != 10 || task.Sort != 3 {
		t.Fatalf("时长/积分/排序错: %d / %d / %d", task.PlannedMinutes, task.BasePoints, task.Sort)
	}
	if !task.AllowOvertime || task.AllowLateNoPenalty || !task.QualityRated {
		t.Fatalf("三个布尔没拆对: overtime=%v lateNoPenalty=%v quality=%v",
			task.AllowOvertime, task.AllowLateNoPenalty, task.QualityRated)
	}
	if task.RemindAt != "18:30" {
		t.Fatalf("提醒时间没拆: %q", task.RemindAt)
	}
	// 已经落到列上的键不该再留在 Extra 里（否则列和 Extra 各存一份，改一边另一边不变）
	if task.Extra != "" {
		t.Fatalf("Extra 应该被剔空，实得 %q", task.Extra)
	}
}

// 签到任务：开关与目标次数都要落列。
func TestTaskFromTemplateItemCheckIn(t *testing.T) {
	it := entities.TemplateItem{
		Name: "练字签到", Category: "art", Cycle: "weekly",
		PlannedMinutes: 15, BasePoints: 5,
		Extra: `{"checkInEnabled":true,"checkInTargetCount":5}`,
	}
	task := TaskFromTemplateItem(1, it)
	if !task.CheckInEnabled || task.CheckInTargetCount != 5 {
		t.Fatalf("签到没拆对: enabled=%v target=%d", task.CheckInEnabled, task.CheckInTargetCount)
	}
}

// ★ 开关关着、但目标次数有值（官方种子里的「背诵」就是 2）：
// 目标次数必须**保留**，不能被当成「没填」清成 0。
func TestTaskFromTemplateItemKeepsTargetWhenCheckInOff(t *testing.T) {
	it := entities.TemplateItem{
		Name: "背诵", Category: "study", Cycle: "weekly",
		PlannedMinutes: 15, BasePoints: 10,
		Extra: `{"checkInEnabled":false,"checkInTargetCount":2}`,
	}
	task := TaskFromTemplateItem(1, it)
	if task.CheckInEnabled {
		t.Fatal("开关应该是关的")
	}
	if task.CheckInTargetCount != 2 {
		t.Fatalf("目标次数被吞了: %d", task.CheckInTargetCount)
	}
}

// 奖励道具 / 时间窗 / 未知玩法参数
func TestTaskFromTemplateItemKeepsRewardsAndUnknownExtra(t *testing.T) {
	it := entities.TemplateItem{
		Name: "读一本完整的故事书", Category: "reading", Cycle: "monthly",
		PlannedMinutes: 40, BasePoints: 15,
		Extra: `{"rewardItemIds":["sticker","medal"],"windowStartMinute":1080,"windowEndMinute":1260,"checkInTierRatio":1.5}`,
	}
	task := TaskFromTemplateItem(1, it)

	if len(task.RewardItemIDs) != 2 || task.RewardItemIDs[0] != "sticker" || task.RewardItemIDs[1] != "medal" {
		t.Fatalf("奖励道具没拆: %v", task.RewardItemIDs)
	}
	if task.WindowStartMinute == nil || *task.WindowStartMinute != 1080 {
		t.Fatalf("时间窗起点没拆: %v", task.WindowStartMinute)
	}
	if task.WindowEndMinute == nil || *task.WindowEndMinute != 1260 {
		t.Fatalf("时间窗终点没拆: %v", task.WindowEndMinute)
	}
	// 服务端不解释的玩法参数必须原样留着，否则家长改了「阶梯倍率」重启就没了
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(task.Extra), &extra); err != nil {
		t.Fatalf("Extra 不是合法 JSON: %q", task.Extra)
	}
	if extra["checkInTierRatio"] != 1.5 {
		t.Fatalf("未知玩法参数被吞了: %v", extra)
	}
	if _, ok := extra["rewardItemIds"]; ok {
		t.Fatal("已落列的 rewardItemIds 不该再留在 Extra 里")
	}
}

// 脏数据兜底：周期/分类非法、时长/积分为 0、Extra 是坏 JSON。
// 这里每一条都会让「任务看得见点不动」或者「结算 0 分」—— 不报错，必须兜。
func TestTaskFromTemplateItemFallsBackOnBadData(t *testing.T) {
	it := entities.TemplateItem{
		Name: "坏任务", Category: "nonsense", Cycle: "hourly",
		PlannedMinutes: 0, BasePoints: -5,
		Extra: `{not json`,
	}
	task := TaskFromTemplateItem(1, it)
	if task.Cycle != entities.CycleDaily {
		t.Fatalf("非法周期没兜底: %s", task.Cycle)
	}
	if task.Category != entities.CategoryStudy {
		t.Fatalf("非法分类没兜底: %s", task.Category)
	}
	if task.PlannedMinutes != 30 || task.BasePoints != 10 {
		t.Fatalf("零值没兜底: %d / %d", task.PlannedMinutes, task.BasePoints)
	}
	if task.Extra != "" {
		t.Fatalf("坏 JSON 应该被丢掉，实得 %q", task.Extra)
	}
}

// 签到开着但没填目标次数 ⇒ 至少 1，否则「一周签 0 次」直接算达标。
func TestTaskFromTemplateItemCheckInTargetAtLeastOne(t *testing.T) {
	task := TaskFromTemplateItem(1, entities.TemplateItem{
		Name: "签到", Category: "habit", Cycle: "weekly",
		PlannedMinutes: 5, BasePoints: 5,
		Extra: `{"checkInEnabled":true}`,
	})
	if task.CheckInTargetCount != 1 {
		t.Fatalf("目标次数应该是 1，实得 %d", task.CheckInTargetCount)
	}
}

// 兑换品：价格 / 库存 / 分类 / 一次性
func TestRedeemItemFromTemplateItem(t *testing.T) {
	it := entities.TemplateItem{
		ItemID:      " official-redeem-01 ",
		Name:        "一份小零食",
		Emoji:       "🍪",
		Category:    "snack",
		Price:       30,
		Note:        "饼干、酸奶、水果，任选一样",
		LimitPerDay: 2,
		Stock:       -1,
		Sort:        4,
	}
	item := RedeemItemFromTemplateItem(9, it)
	if item.UID != 9 || item.ItemID != "official-redeem-01" {
		t.Fatalf("归属/业务 ID 错: uid=%d id=%q", item.UID, item.ItemID)
	}
	if item.Price != 30 || item.LimitPerDay != 2 || item.Stock != -1 || item.Sort != 4 {
		t.Fatalf("价格/限次/库存/排序错: %+v", item)
	}
	if item.Category != entities.RedeemCategorySnack || item.Emoji != "🍪" {
		t.Fatalf("分类/图标错: %s %s", item.Category, item.Emoji)
	}
	if !item.CreatedByParent {
		t.Fatal("模板铺出来的兑换品应该标成家长创建（孩子不能改价）")
	}
}

// ★ 模板里 stock 的零值：绝大多数是「后台没填」，不是「库存 0」。
// 当成 0 的话，整份默认清单会变成「一件都换不了」，而且不报错。
func TestRedeemItemFromTemplateItemZeroStockMeansUnlimited(t *testing.T) {
	item := RedeemItemFromTemplateItem(1, entities.TemplateItem{
		ItemID: "x", Name: "x", Category: "gift", Price: 10, Stock: 0,
	})
	if item.Stock != -1 {
		t.Fatalf("库存 0 应该按无限处理，实得 %d", item.Stock)
	}
}

// 脏数据兜底：分类非法 → custom；负价 → 0
func TestRedeemItemFromTemplateItemFallsBack(t *testing.T) {
	item := RedeemItemFromTemplateItem(1, entities.TemplateItem{
		ItemID: "y", Name: "y", Category: "nonsense", Price: -20, Stock: 3,
	})
	if item.Category != entities.RedeemCategoryCustom {
		t.Fatalf("非法分类没兜底: %s", item.Category)
	}
	if item.Price != 0 {
		t.Fatalf("负价没兜底: %d", item.Price)
	}
	if item.Stock != 3 {
		t.Fatalf("正数库存不该被动: %d", item.Stock)
	}
}

func TestIsDuplicateKey(t *testing.T) {
	cases := map[string]bool{
		"UNIQUE constraint failed: template_apply.uid":   true,
		"Error 1062: Duplicate entry '3' for key 'uid'":  true,
		"no such table: template_apply":                  false,
		"database or disk is full (13)":                  false,
	}
	for msg, want := range cases {
		if got := isDuplicateKey(errString(msg)); got != want {
			t.Fatalf("%q: 期望 %v，实得 %v", msg, want, got)
		}
	}
	if isDuplicateKey(nil) {
		t.Fatal("nil 不该算唯一键冲突")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
