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
	// RandomGift 为 true 时 **GameID 不参与判定**：领奖那一刻现挑一款
	// 用户还没拥有的 available 游戏（见 service 的 pickRandomGift）。
	// 对应用户 2026-11-14：「第5关 完成 50个任务 奖励随机一个没拥有的游戏。」
	RandomGift bool
}

// campaignSteps 主线关卡白名单。
//
// ⚠️ **奖励不能选默认免费的游戏** —— 那样这一关「一开局就能领、领完什么都不多」。
//
//	2026-11-14 用户明确要求第 2 关送「云边小农场」，而它原本是 `default_owned=1`。
//	所以同一批改动里把 seed 的 `DefaultOwned` 改成 false（并 UPDATE 线上库）。
//	**已有的 `user_game` 记录不回收** ⇒ 老用户不受影响，新用户才靠这一关解锁。
var campaignSteps = []CampaignStep{
	{StepID: "first5", Metric: CampaignMetricTaskDone, Target: 5, GameID: "fall2048", GameName: "下落的2048"},
	// ★ 用户 2026-11-14 指定送云边小农场。它原来是 default_owned=1（登录即拥有），
	//   所以同一批改动里把 seed 的 DefaultOwned 改成 false —— 否则这一关是白送。
	{StepID: "coupon100", Metric: CampaignMetricCouponEarned, Target: 100, GameID: "cloudfarm", GameName: "云边小农场"},
	{StepID: "first15", Metric: CampaignMetricTaskDone, Target: 15, GameID: "screamchick", GameName: "尖叫鸡"},
	{StepID: "coupon300", Metric: CampaignMetricCouponEarned, Target: 300, GameID: "hoopsneak", GameName: "偷偷投篮"},
	// 第 5 关：随机一款未拥有。GameID 只是兜底文案，真正的游戏由 pickRandomGift 现挑。
	{StepID: "first50", Metric: CampaignMetricTaskDone, Target: 50, GameID: "", GameName: "随机一款你还没有的", RandomGift: true},
	{StepID: "coupon600", Metric: CampaignMetricCouponEarned, Target: 600, GameID: "blockfit", GameName: "方块填空"},
	{StepID: "first100", Metric: CampaignMetricTaskDone, Target: 100, GameID: "prizepark", GameName: "抓奖乐园"},
	{StepID: "coupon1000", Metric: CampaignMetricCouponEarned, Target: 1000, GameID: "claw3d", GameName: "抓娃娃"},
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
