package entities

// ============================ 农场状态 ============================

// FarmState 一个用户的农场状态（整块 JSON）。
//
// ## 为什么是整块 JSON 而不是结构化表
//
// 客户端的农场是一整套**相互耦合**的对象：地块（作物 + 生长起点 + 额度）、
// 动物（出生时间 + 产出进度 + 生病）、背包、行情、世界日、额度结转、事件日志。
// 拆成 8 张表意味着服务端要理解每一处玩法规则，而玩法还在改
// （2026-11 还在调行情曲线和额度结转）。
//
// 所以这里和 `game_kv` 同一个取舍：**服务端不理解内容，只负责「谁的那一份」**。
// 与 `game_kv` 的差别是它有独立的表和乐观锁（`rev`），因为它是**每个用户唯一一份**
// 且会被频繁整块覆盖 —— 塞进 kv 会和「某款游戏的存档」共用一个配额与命名空间。
//
// ## rev 是什么
//
// 客户端每次保存都带上自己拿到的 `rev`，服务端只有 `rev` 相等时才写入。
// 两台设备同时改（爸爸在手机上收菜、平板还开着）时，后到的那次会拿到
// `conflict=true`，客户端重新拉一次再改 —— 而不是**静默把对方的一次收获吞掉**。
type FarmState struct {
	BaseModel

	UID uint `gorm:"column:uid;uniqueIndex;not null" json:"uid"`

	// Rev 乐观锁版本号，每次成功保存 +1
	Rev int64 `gorm:"column:rev;default:0;not null" json:"rev"`

	// Payload 农场状态（任意 JSON，客户端定义结构）
	Payload string `gorm:"column:payload;type:LONGTEXT;not null" json:"payload"`

	// Size 字节数（列表/统计用，省得每次算 length）
	Size int64 `gorm:"column:size;default:0;not null" json:"size"`
}

func (FarmState) TableName() string { return "farm_state" }

// ============================ 农场状态 DTO ============================

// FarmStateMaxPayload 单次保存允许的最大字节数。
//
// 实测客户端一份完整农场（12 块地 + 8 只动物 + 背包 + 120 条事件）约 40 KB，
// 给到 1 MB 是留足余量、同时挡住「把整个 IndexedDB 塞进来」这种误用。
const FarmStateMaxPayload = 1 << 20

// FarmStateGetResp 读取农场状态
type FarmStateGetResp struct {
	// Empty 该用户还没保存过（客户端应当用本地初始农场播种）
	Empty bool `json:"empty"`
	// Rev 当前版本；`Empty=true` 时为 0
	Rev       int64  `json:"rev"`
	Payload   string `json:"payload"`
	UpdatedAt int64  `json:"updated_at"`
}

// FarmLedgerClaim 随农场状态一起提交的一笔币变动。
//
// ## 为什么账本要跟着农场一起提交
//
// 「卖菜得兑换券」这件事在客户端算（行情、额度、品质都在本地规则里），
// 服务端只收到结果。如果拆成「先存农场、再单独记账」两次请求，
// 中间断网就是「菜没了、券没到」——而孩子看不出是网络问题。
//
// 放进同一个事务：要么农场和账本都成，要么都不成。
//
// ## OpID 是幂等键
//
// 客户端每次操作生成一个随机 id。重试（断网重发、用户连点）时服务端
// 认出同一笔就不再加一次 —— 否则孩子点两下「卖出」会拿到两份券。
type FarmLedgerClaim struct {
	// Currency points / coupon / diamond
	Currency string `json:"currency" binding:"required"`
	// Delta 正数收入、负数支出
	Delta int64 `json:"delta"`
	// Source 账本来源（farm_harvest / animal_produce / seed_purchase …）
	Source string `json:"source"`
	Memo   string `json:"memo"`
	// OpID 幂等键，同一 OpID 只会入账一次
	OpID string `json:"op_id" binding:"required,max=64"`
}

// FarmStateSaveReq 保存农场状态
type FarmStateSaveReq struct {
	Payload string `json:"payload" binding:"required"`
	// Rev 客户端持有的版本（0 = 首次保存）
	Rev int64 `json:"rev"`
	// Ledger 本次操作产生的币变动（可以为空）
	Ledger []FarmLedgerClaim `json:"ledger"`
	// Force 版本冲突时强制覆盖。
	// 客户端**只在重新拉过一次之后**才该用它，否则等于放弃冲突检测。
	Force bool `json:"force"`
}

// FarmStateSaveResp 保存结果
type FarmStateSaveResp struct {
	// Conflict 版本冲突（本次**没有**写入，客户端应重新拉取）
	Conflict bool `json:"conflict"`
	// Rev 服务端当前的版本（冲突时是别人的版本，成功时是新版本）
	Rev int64 `json:"rev"`
	// Applied 实际入账的笔数（幂等命中的不计）
	Applied int `json:"applied"`
	// Balances 入账后的三套币余额（省掉客户端再查一次钱包）
	Balances map[string]int64 `json:"balances"`
}
