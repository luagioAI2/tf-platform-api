package entities

// ============================ 模板落地（一次性） ============================
//
// `template` / `template_item` 是**蓝图**，`task` / `redeem_item` 是**实例**。
// 这一层负责「第一次进来时按蓝图铺出实例」，并且**只铺一次**。

// TemplateApply 记录「这个账号已经按模板铺过一次初始内容」。
//
// ## 为什么必须有这张表，而不是「任务表为空就铺」
//
// 家长把任务全删了（或者只留一条再删），任务表同样为空 —— 拿「空」当判据，
// 下次启动就把 16 条官方任务又塞回去，而且**不报错**。
// 孩子的感受是「删掉的东西自己长回来了」，家长的感受是「这 App 有毛病」。
// 所以判据必须是「这个账号铺过没有」，不是「表现在空不空」。
//
// ## 唯一键就是锁
//
// `uid` 上有唯一索引：并发两次 apply（StrictMode 会把启动 effect 跑两遍、
// 两台设备同时首启）时，第二次的 INSERT 直接冲突 —— 不需要额外的分布式锁，
// SQLite 和 MySQL 都一样。
type TemplateApply struct {
	BaseModel

	UID uint `gorm:"column:uid;uniqueIndex:uk_uid_tpl_apply;not null" json:"uid"`

	// TaskTemplateID / RedeemTemplateID 这次实际用的是哪份模板。
	// 0 = 该类目没铺（账号本来就有内容，或者服务端连模板都没有）。
	TaskTemplateID   uint `gorm:"column:task_template_id;default:0" json:"task_template_id"`
	RedeemTemplateID uint `gorm:"column:redeem_template_id;default:0" json:"redeem_template_id"`

	// TaskCount / RedeemCount 本次实际写进去的条数（0 = 跳过了这一类）。
	TaskCount   int `gorm:"column:task_count;default:0" json:"task_count"`
	RedeemCount int `gorm:"column:redeem_count;default:0" json:"redeem_count"`

	// Skipped 为什么没铺：空 = 铺了；`has-content` = 账号本来就有内容。
	// 只用于排查「这个新用户怎么没有任务」，不参与任何判定。
	Skipped string `gorm:"column:skipped;size:32" json:"skipped"`
}

func (TemplateApply) TableName() string { return "template_apply" }

// TemplateApplyResp 落地结果。
//
// `Applied=false` 不是错误：绝大多数启动都会走到这条（账号早就铺过了），
// 客户端不该弹任何东西。
type TemplateApplyResp struct {
	Applied           bool   `json:"applied"`
	Reason            string `json:"reason"`
	TaskTemplateID    uint   `json:"task_template_id"`
	RedeemTemplateID  uint   `json:"redeem_template_id"`
	TaskCount         int    `json:"task_count"`
	RedeemCount       int    `json:"redeem_count"`
	SourceMerchantUID uint   `json:"source_merchant_uid"`
}

// 落地结果里的 reason（也是 TemplateApply.Skipped 的取值）
const (
	// ApplyReasonOK 本次铺了
	ApplyReasonOK = ""
	// ApplyReasonAlready 这个账号以前铺过（唯一键冲突 / 表里已有行）
	ApplyReasonAlready = "already"
	// ApplyReasonHasContent 账号里本来就有任务或兑换品 ⇒ 一条都不动
	ApplyReasonHasContent = "has-content"
)
