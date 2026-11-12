package entities

/* ============================================================
   主线活动（Campaign）
   ------------------------------------------------------------
   客户端 2026-11-12 加了「主线活动」：完成 N 个任务 / 累计赚够 N 张兑换券，
   就**免费激活**一款游戏。用户原话：

       「添加活动模块，比如完成 5 个任务免费激活下落的2048，
        比如兑换卷满 100 激活云边小农场，类似主线任务。」

   ## 为什么服务端必须自己有一份关卡表

   在线版的「拥有关系」只认 `user_game`。客户端自己写一笔本地账本**没用**：
   换台设备就没了，而且存档写不进去（`checkGameExists` 要 `user_game` 有行）。
   所以领奖必须由服务端授游戏，而**授哪一款、够不够条件**只能服务端自己判。

   ⚠️ 因此关卡表在**两个地方**各有一份：
       客户端 `src/domain/campaign.ts`（负责文案/进度展示，本地版也用它）
       本文件 `campaignSteps`（负责真的授不授）
      `StepID` 是两边的**唯一契约**，改一个字等于让老客户的关卡作废。
      加关卡时两边一起改，并跑一次
      `scripts/_probe-campaign-parity.mjs`（比对两边的 id 集合）。

   ## 这里是「白名单」，不是「客户端传参」

   `Claim` 只收 `step_id`，**进度、目标值、奖励游戏都不从请求里读**。
   让客户端传这些等于让它自己决定送哪款游戏。
   ============================================================ */

// CampaignMetric 进度指标。字符串值与客户端 `CampaignMetric` 一字不差。
type CampaignMetric string

const (
	// CampaignMetricTaskDone 完成的任务数（completed + failed）
	CampaignMetricTaskDone CampaignMetric = "task_done"
	// CampaignMetricCouponEarned 累计**赚到**的兑换券（不是当前余额）
	CampaignMetricCouponEarned CampaignMetric = "coupon_earned"
)

// CampaignStep 一关的定义。**顺序就是界面顺序。**
type CampaignStep struct {
	StepID   string
	Metric   CampaignMetric
	Target   int64
	GameID   string
	GameName string // 只用于出错时的人话提示，不参与判定
}

// campaignSteps 主线关卡白名单。
//
// ⚠️ **奖励不能选默认免费的游戏**（现在 `cloudfarm` 云边小农场是 `default_owned=1`）。
//
//	用户举例时正好举了它 —— 照抄会做出一关「一开局就能领、领完什么都不多」。
//	所以「券满 100」那一关送的是「收租公寓」。
var campaignSteps = []CampaignStep{
	{StepID: "first5", Metric: CampaignMetricTaskDone, Target: 5, GameID: "fall2048", GameName: "下落的2048"},
	{StepID: "coupon100", Metric: CampaignMetricCouponEarned, Target: 100, GameID: "renttower", GameName: "收租公寓"},
	{StepID: "first15", Metric: CampaignMetricTaskDone, Target: 15, GameID: "blockfit", GameName: "方块填空"},
	{StepID: "coupon300", Metric: CampaignMetricCouponEarned, Target: 300, GameID: "hoopsneak", GameName: "偷偷投篮"},
}

// CampaignStepByID 按 id 取一关（找不到返回 nil）
func CampaignStepByID(stepID string) *CampaignStep {
	for i := range campaignSteps {
		if campaignSteps[i].StepID == stepID {
			return &campaignSteps[i]
		}
	}
	return nil
}

// CampaignSteps 返回全部关卡（顺序固定，调用方别排序）
func CampaignSteps() []CampaignStep { return campaignSteps }

// ============================ 领取记录 ============================

// CampaignClaim 一关的领取记录。
//
// 唯一索引建在 `(uid, step_id)` 上，不是 `(uid, game_id)` ——
// 两关以后可能送同一款游戏（改配置时容易出现），按游戏去重会让人领不到第二关。
type CampaignClaim struct {
	BaseModel

	UID uint `gorm:"column:uid;uniqueIndex:uk_uid_step;not null" json:"uid"`
	// StepID 关卡 id（`first5` …），与客户端 `CAMPAIGN_STEPS[i].id` 同一个值
	StepID string `gorm:"column:step_id;size:32;uniqueIndex:uk_uid_step;not null" json:"step_id"`
	// GameID 这一关实际授了哪款（**记下来是为了事后查**：配置改了也不会说不清）
	GameID string `gorm:"column:game_id;size:32;not null" json:"game_id"`
}

func (CampaignClaim) TableName() string { return "campaign_claim" }

// ============================ DTO ============================

// CampaignStateResp 客户端拉「领了哪几关 + 服务端算的进度」
type CampaignStateResp struct {
	Steps []CampaignStepView `json:"steps"`
	// 两个指标一起下发，方便客户端对账（它自己也有一份算法）
	TasksDone     int64 `json:"tasks_done"`
	CouponsEarned int64 `json:"coupons_earned"`
}

// CampaignStepView 一关的服务端判定
type CampaignStepView struct {
	ID      string `json:"id"`
	GameID  string `json:"game_id"`
	Claimed bool   `json:"claimed"`
	Have    int64  `json:"have"`
	Target  int64  `json:"target"`
}

// CampaignClaimReq 领奖请求。**只有 step_id** —— 别的都不采信。
type CampaignClaimReq struct {
	StepID string `json:"step_id" binding:"required,max=32"`
}

// CampaignClaimResp 领奖结果
type CampaignClaimResp struct {
	StepID string `json:"step_id"`
	GameID string `json:"game_id"`
	// Already 之前就领过（幂等：重复点不会报错，也不会重复授）
	Already bool `json:"already"`
}
