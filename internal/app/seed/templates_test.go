package seed

import (
	"encoding/json"
	"testing"
)

// 官方模板种子是从 App 的 seedTasks.ts 手抄过来的，最容易出的错是
// 「少一条 / 多一条 / JSON 里少个逗号」—— 而这类错在启动日志里只是
// 一句 Warn，服务照常起来，界面上却是「默认任务少了」。所以钉死条数。
func TestOfficialSeedShape(t *testing.T) {
	var s officialSeed
	if err := json.Unmarshal([]byte(officialSeedJSON), &s); err != nil {
		t.Fatalf("种子 JSON 解析失败: %v", err)
	}
	if len(s.Tasks) != 16 {
		t.Fatalf("官方任务模板应有 16 条，实际 %d 条", len(s.Tasks))
	}
	if len(s.RedeemItems) != 15 {
		t.Fatalf("官方兑换模板应有 15 条，实际 %d 条", len(s.RedeemItems))
	}
	for i, row := range s.Tasks {
		if str(row["title"]) == "" || str(row["category"]) == "" || str(row["cycle"]) == "" {
			t.Fatalf("任务第 %d 条缺 title/category/cycle: %v", i+1, row)
		}
		if num(row["basePoints"]) <= 0 {
			t.Fatalf("任务第 %d 条 basePoints 非正: %v", i+1, row)
		}
	}
	for i, row := range s.RedeemItems {
		if str(row["name"]) == "" || str(row["category"]) == "" {
			t.Fatalf("兑换第 %d 条缺 name/category: %v", i+1, row)
		}
		if num64(row["cost"]) <= 0 {
			t.Fatalf("兑换第 %d 条 cost 非正: %v", i+1, row)
		}
	}
}

func TestTaskItemFromSeedKeepsExtra(t *testing.T) {
	var s officialSeed
	_ = json.Unmarshal([]byte(officialSeedJSON), &s)
	it := taskItemFromSeed(0, s.Tasks[0])
	if it.Name != "完成语文作业" || it.Cycle != "daily" || it.BasePoints != 10 {
		t.Fatalf("typed 列映射错: %+v", it)
	}
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(it.Extra), &extra); err != nil {
		t.Fatalf("Extra 不是合法 JSON: %v", err)
	}
	if extra["allowOvertime"] != true || extra["remindAt"] != "18:30" {
		t.Fatalf("Extra 丢了玩法参数: %v", extra)
	}
	if _, ok := extra["title"]; ok {
		t.Fatalf("typed 列不该再进 Extra（否则后台改了会被旧值盖掉）: %v", extra)
	}
}

func TestRedeemItemFromSeedKeepsExtra(t *testing.T) {
	var s officialSeed
	_ = json.Unmarshal([]byte(officialSeedJSON), &s)
	it := redeemItemFromSeed(0, s.RedeemItems[0])
	if it.Name != "一份小零食" || it.Price != 30 || it.LimitPerDay != 2 {
		t.Fatalf("typed 列映射错: %+v", it)
	}
	var extra map[string]interface{}
	_ = json.Unmarshal([]byte(it.Extra), &extra)
	if extra["createdByParent"] != true {
		t.Fatalf("Extra 丢了 createdByParent: %v", extra)
	}
}
