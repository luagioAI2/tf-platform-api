package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tf-api/internal/app/entities"

	"gorm.io/gorm"
)

// ============================ 官方默认模板种子 ============================
//
// 数据来源：`kid-quest-farm-2d` / `kid-quest-farm-online` 的
// `src/domain/seedTasks.ts`（SEED_TASKS / SEED_REDEEM_ITEMS）。
//
// ★ 这份 JSON 保持 **App 原生字段名**（camelCase）。写入时把有对应列的
//   字段拆到 template_item 的列里，剩下的原样塞进 Extra —— 这样后台改了
//   「基础积分」不会丢掉 `checkInTargetCount`、`rewardItemIds` 这些玩法参数。
//
// ★ 幂等口径：**只要该 kind 已经有官方模板就整体跳过**。
//   运营在后台改过官方模板之后，服务重启不许把它覆盖回种子（那会让
//   「我明明改了怎么又变回去了」，而且不报错）。

const officialSeedJSON = `{
  "tasks": [
    {"title":"完成语文作业","note":"认真写，字要工整哦","category":"study","cycle":"daily","plannedMinutes":45,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"remindAt":"18:30"},
    {"title":"完成数学作业","note":"先做会的，不会的想一想再问","category":"study","cycle":"daily","plannedMinutes":45,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"remindAt":"19:00"},
    {"title":"完成英语作业","note":"写完读一遍，单词顺手背了","category":"study","cycle":"daily","plannedMinutes":45,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"remindAt":"19:30"},
    {"title":"语文课外练习","note":"阅读理解、看图写话都可以","category":"study","cycle":"daily","plannedMinutes":45,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true},
    {"title":"数学课外练习","note":"错的题在错题本上再算一遍","category":"study","cycle":"daily","plannedMinutes":45,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true},
    {"title":"英语课外练习","note":"读一读、写一写，大声念出来","category":"study","cycle":"daily","plannedMinutes":45,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true},
    {"title":"清理自己的房间","note":"书桌、床、玩具都要归位","category":"chore","cycle":"daily","plannedMinutes":15,"basePoints":6,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true},
    {"title":"练字签到","note":"每天写一页，坚持一周有大奖励","category":"art","cycle":"weekly","plannedMinutes":15,"basePoints":5,"allowOvertime":false,"allowLateNoPenalty":true,"qualityRated":false,"checkInEnabled":true,"checkInTargetCount":5},
    {"title":"日记","note":"今天发生了什么？写几句话就好","category":"art","cycle":"weekly","plannedMinutes":15,"basePoints":5,"allowOvertime":false,"allowLateNoPenalty":true,"qualityRated":false,"checkInEnabled":true,"checkInTargetCount":5},
    {"title":"晨读","note":"早上大声读一会儿，一天都精神","category":"reading","cycle":"weekly","plannedMinutes":15,"basePoints":5,"allowOvertime":false,"allowLateNoPenalty":true,"qualityRated":false,"checkInEnabled":true,"checkInTargetCount":5},
    {"title":"数学计算练习","note":"每天一页口算或竖式，掐着表做更快","category":"study","cycle":"weekly","plannedMinutes":15,"basePoints":5,"allowOvertime":false,"allowLateNoPenalty":true,"qualityRated":false,"checkInEnabled":true,"checkInTargetCount":5},
    {"title":"学习日复述每天学习内容","note":"放学问他一句：今天学了什么？讲给你听","category":"study","cycle":"weekly","plannedMinutes":10,"basePoints":5,"allowOvertime":false,"allowLateNoPenalty":true,"qualityRated":false,"checkInEnabled":true,"checkInTargetCount":5},
    {"title":"背诵","note":"古诗、课文都行，背完讲给爸爸妈妈听","category":"study","cycle":"weekly","plannedMinutes":15,"basePoints":10,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"checkInEnabled":false,"checkInTargetCount":2},
    {"title":"读一本完整的故事书","note":"厚一点也没关系，慢慢读","category":"reading","cycle":"monthly","plannedMinutes":40,"basePoints":15,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"checkInEnabled":false,"checkInTargetCount":4,"rewardItemIds":["sticker"]},
    {"title":"学会一项新本领","note":"比如游泳、骑车、魔方……今年内学会就行","category":"habit","cycle":"yearly","plannedMinutes":60,"basePoints":50,"allowOvertime":true,"allowLateNoPenalty":true,"qualityRated":true,"checkInTargetCount":6,"rewardItemIds":["medal"]},
    {"title":"每月记忆单词","note":"每月背新单词，积少成多 📚","category":"study","cycle":"yearly","plannedMinutes":30,"basePoints":20,"allowOvertime":true,"allowLateNoPenalty":false,"qualityRated":true,"checkInTargetCount":12,"rewardItemIds":["medal"]}
  ],
  "redeem_items": [
    {"name":"一份小零食","emoji":"🍪","category":"snack","cost":30,"note":"饼干、酸奶、水果，任选一样","limitPerDay":2,"createdByParent":true},
    {"name":"15 分钟自由休息时间","emoji":"🛋️","category":"privilege","cost":30,"note":"想歇一会儿就歇，自己定个闹钟","limitPerDay":2,"createdByParent":true},
    {"name":"一杯喜欢的饮料","emoji":"🧃","category":"snack","cost":45,"note":"果汁或者牛奶","limitPerDay":1,"createdByParent":true},
    {"name":"玩 30 分钟平板","emoji":"📱","category":"screen","cost":60,"note":"自己定闹钟，时间到了就收起来","limitPerDay":2,"createdByParent":true},
    {"name":"2 小时电视时间","emoji":"🎬","category":"screen","cost":180,"note":"一次看完两小时，自己关掉","limitPerDay":1,"createdByParent":true},
    {"name":"看一集动画片","emoji":"📺","category":"screen","cost":50,"note":"一集，看完就关","limitPerDay":2,"createdByParent":true},
    {"name":"晚睡 30 分钟","emoji":"🌙","category":"privilege","cost":30,"note":"周末才能用哦","limitPerDay":1,"createdByParent":true},
    {"name":"去朋友家玩半天","emoji":"🏃","category":"outing","cost":200,"note":"要提前跟爸爸妈妈约时间","limitPerDay":1,"createdByParent":true},
    {"name":"一起去公园 / 游乐场","emoji":"🎡","category":"outing","cost":300,"note":"挑个好天气去","limitPerDay":1,"createdByParent":true},
    {"name":"挑一个小玩具","emoji":"🧸","category":"gift","cost":600,"note":"和爸爸妈妈一起选，预算内自己定","limitPerDay":1,"createdByParent":true},
    {"name":"挑一个大玩具","emoji":"🎠","category":"gift","cost":1200,"note":"攒久一点，换个大的","limitPerDay":1,"createdByParent":true},
    {"name":"点一次想要的晚餐","emoji":"🍕","category":"privilege","cost":260,"note":"今天吃什么你说了算","limitPerDay":1,"createdByParent":true},
    {"name":"一次「免做一项家务」","emoji":"🧹","category":"privilege","cost":150,"note":"任何一项家务都可以，用掉就没了","limitPerDay":1,"createdByParent":true},
    {"name":"和爸爸妈妈独处的一下午","emoji":"💛","category":"privilege","cost":180,"note":"想去哪、做什么，你决定","limitPerDay":1,"createdByParent":true},
    {"name":"一个大愿望","emoji":"🎁","category":"gift","cost":1500,"note":"攒够了一起商量，比如一次短途旅行","limitPerDay":1,"createdByParent":true}
  ]
}`

type officialSeed struct {
	Tasks       []map[string]interface{} `json:"tasks"`
	RedeemItems []map[string]interface{} `json:"redeem_items"`
}

type itemBuilder func(i int, row map[string]interface{}) entities.TemplateItem

// EnsureTemplates 幂等写入两份官方默认模板（仅开发/测试环境调用）。
func EnsureTemplates(ctx context.Context, db *gorm.DB) error {
	var s officialSeed
	if err := json.Unmarshal([]byte(officialSeedJSON), &s); err != nil {
		return fmt.Errorf("parse official template seed: %w", err)
	}
	if err := ensureOne(ctx, db, entities.TemplateKindTask, "官方任务模板", s.Tasks, taskItemFromSeed); err != nil {
		return err
	}
	if err := ensureOne(ctx, db, entities.TemplateKindRedeem, "官方兑换模板", s.RedeemItems, redeemItemFromSeed); err != nil {
		return err
	}
	return nil
}

func ensureOne(ctx context.Context, db *gorm.DB, kind entities.TemplateKind, name string,
	rows []map[string]interface{}, build itemBuilder) error {
	var existing int64
	if err := db.WithContext(ctx).Model(&entities.Template{}).
		Where("scope = ? AND kind = ? AND owner_uid = 0",
			string(entities.TemplateScopeOfficial), string(kind)).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil // 已经有官方模板（可能是运营改过的）：不覆盖
	}

	now := time.Now().Unix()
	tpl := entities.Template{
		// ⚠️ autoCreateTime:false —— 不手写就是 1970 年，且列表按时间排全在最后
		BaseModel: entities.BaseModel{CreatedAt: now, UpdatedAt: now},
		Name:      name,
		Kind:      kind,
		Scope:     entities.TemplateScopeOfficial,
		OwnerUID:  0,
		IsDefault: true,
		Note:      "App 首次启动的默认内容，可在后台修改",
		Sort:      0,
		Source:    "seed",
	}
	if err := db.WithContext(ctx).Create(&tpl).Error; err != nil {
		return err
	}
	for i, row := range rows {
		it := build(i, row)
		it.BaseModel = entities.BaseModel{CreatedAt: now, UpdatedAt: now}
		it.TemplateID = tpl.ID
		if err := db.WithContext(ctx).Create(&it).Error; err != nil {
			return err
		}
	}
	return nil
}

func taskItemFromSeed(i int, row map[string]interface{}) entities.TemplateItem {
	return entities.TemplateItem{
		ItemID:         fmt.Sprintf("official-task-%02d", i+1),
		Name:           str(row["title"]),
		Category:       str(row["category"]),
		Cycle:          str(row["cycle"]),
		PlannedMinutes: num(row["plannedMinutes"]),
		BasePoints:     num(row["basePoints"]),
		Note:           str(row["note"]),
		Stock:          -1,
		Sort:           i,
		Extra:          omit(row, "title", "note", "category", "cycle", "plannedMinutes", "basePoints"),
	}
}

func redeemItemFromSeed(i int, row map[string]interface{}) entities.TemplateItem {
	return entities.TemplateItem{
		ItemID:      fmt.Sprintf("official-redeem-%02d", i+1),
		Name:        str(row["name"]),
		Emoji:       str(row["emoji"]),
		Category:    str(row["category"]),
		Price:       num64(row["cost"]),
		Note:        str(row["note"]),
		LimitPerDay: num(row["limitPerDay"]),
		Stock:       -1,
		Sort:        i,
		Extra:       omit(row, "name", "emoji", "category", "cost", "note", "limitPerDay"),
	}
}

func str(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func num(v interface{}) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

func num64(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	}
	return 0
}

// omit 把 row 里除 keys 之外的键原样序列化成 Extra（保持 camelCase）。
func omit(row map[string]interface{}, keys ...string) string {
	out := make(map[string]interface{}, len(row))
	for k, v := range row {
		drop := false
		for _, key := range keys {
			if k == key {
				drop = true
				break
			}
		}
		if !drop {
			out[k] = v
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}
