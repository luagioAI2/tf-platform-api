package entities

// ============================ 存档快照 ============================

// GameKvSnapshot 游戏存档快照。
//
// ## 为什么需要快照
//
// KV 存档是**整块覆盖**：一次 `set` 就把旧值冲掉了。线上化之后有两个现实问题：
//
//  1. **回档**：孩子误操作（把农场推平了）、游戏 bug 把存档写坏了、
//     或者家长想把进度退回某个时间点。
//  2. **审计**：出现「我的金币怎么少了」的争议时，需要能查到历史值。
//
// 前端 Dexie 没有这个问题（数据在本地，导出备份就是全量），
// 但服务端必须自己留历史。
//
// ## 快照策略（不存全量历史，只存关键节点）
//
// 全量存每次写入会爆炸（一个活跃玩家一天可能写几百次）。所以按策略采样：
//
//	| 触发条件          | 是否存快照 |
//	| 距上次快照 > 10 分钟 | 是       |
//	| 值变化 > 20%       | 是       |
//	| 手动标记（回档前）   | 是       |
//	| 其他               | 否       |
//
// 保留策略：每 (uid, game_id, key) 最多保留最近 N 个快照（默认 20），
// 超出按时间淘汰最旧的。另外**永久保留**每天第一个快照（日切基线）。
//
// ## 与 GameKv 的关系
//
// `GameKv` 是当前值，`GameKvSnapshot` 是历史值。
// 回档 = 把某个快照的值写回 GameKv，**并且再存一个快照**（记录回档动作本身）。
type GameKvSnapshot struct {
	BaseModel

	UID    uint   `gorm:"column:uid;index:idx_uid_game_key_at;not null" json:"uid"`
	GameID string `gorm:"column:game_id;size:32;index:idx_uid_game_key_at;not null" json:"game_id"`
	Key    string `gorm:"column:kv_key;size:128;index:idx_uid_game_key_at;not null" json:"key"`

	// Value 快照内容
	Value string `gorm:"column:value;type:LONGTEXT;not null" json:"-"`
	// Size 大小（字节）
	Size int64 `gorm:"column:size;default:0" json:"size"`

	// Version 该快照对应的是哪个版本的值
	Version int64 `gorm:"column:version;default:0" json:"version"`

	// Reason 为什么存这个快照
	//   auto     自动（时间/变化触发）
	//   daily    日切基线
	//   manual   手动（回档前/家长主动保存）
	//   before_restore 回档前
	//   restore  回档动作本身
	Reason string `gorm:"column:reason;size:16;index;not null" json:"reason"`

	// IsDailyBaseline 是否为当天第一个快照（永久保留）
	IsDailyBaseline bool `gorm:"column:is_daily_baseline;default:false;index" json:"is_daily_baseline"`

	// Note 备注（手动快照时可写）
	Note string `gorm:"column:note;size:128" json:"note"`
}

func (GameKvSnapshot) TableName() string { return "game_kv_snapshot" }

// 快照原因
const (
	SnapshotReasonAuto          = "auto"           // 自动（时间/变化触发）
	SnapshotReasonDaily         = "daily"          // 日切基线
	SnapshotReasonManual        = "manual"         // 手动保存
	SnapshotReasonBeforeRestore = "before_restore" // 回档前
	SnapshotReasonRestore       = "restore"        // 回档动作
)

// ============================ 回档记录 ============================

// GameKvRestoreLog 回档操作日志（审计用）。
//
// 为什么要单独一张表而不是只靠快照：回档是**高风险操作**，
// 需要记录「谁、什么时候、把哪个存档、从哪个快照、恢复到了什么状态」。
type GameKvRestoreLog struct {
	BaseModel

	UID    uint   `gorm:"column:uid;index;not null" json:"uid"`
	GameID string `gorm:"column:game_id;size:32;index;not null" json:"game_id"`
	Key    string `gorm:"column:kv_key;size:128;index;not null" json:"key"`

	// SnapshotID 从哪个快照恢复
	SnapshotID uint `gorm:"column:snapshot_id;not null" json:"snapshot_id"`
	// FromVersion 恢复前的版本
	FromVersion int64 `gorm:"column:from_version;default:0" json:"from_version"`
	// ToVersion 恢复后的版本
	ToVersion int64 `gorm:"column:to_version;default:0" json:"to_version"`

	// OperatorID 操作者（0 = 用户自己，>0 = 管理员）
	OperatorID uint `gorm:"column:operator_id;default:0;index" json:"operator_id"`
	// Reason 回档原因
	Reason string `gorm:"column:reason;size:255" json:"reason"`

	// TargetAt 被恢复的那份快照的**创建时间**（Unix 秒）。
	//
	// 为什么要它：家长在界面上看到的是「还原点」，不是「第 3 版」——
	// 只回一句「退回了以前的版本」，他没法知道退回到了哪一天。
	//
	// ⚠️ **必须在回档那一刻写进日志行，不能只在列表时现查**：被恢复的那份快照
	// 会被保留策略淘汰（每个 key 只留 20 份），现查的话过几天历史记录就全变成
	// 「以前的版本」—— 刚修好的东西会自己烂掉。`SnapshotID` 留给审计追溯，
	// 时间这一项要在回档时就固化下来。
	//
	// 老行（加这一列之前写的）这里是 0，列表时按 `snapshot_id` 回填一次；
	// 回填不上（快照已经淘汰）就还是 0，界面退回旧说法，不能显示成 1970。
	TargetAt int64 `gorm:"column:target_at;default:0" json:"target_at"`
}

func (GameKvRestoreLog) TableName() string { return "game_kv_restore_log" }

// ============================ 快照策略常量 ============================

const (
	// SnapshotMinIntervalSeconds 两次自动快照的最小间隔（秒）
	SnapshotMinIntervalSeconds = 600 // 10 分钟
	// SnapshotChangeRatioThreshold 值变化超过这个比例才存快照
	SnapshotChangeRatioThreshold = 0.2 // 20%
	// SnapshotKeepPerKey 每个 key 保留的快照数（不含日切基线）
	SnapshotKeepPerKey = 20
	// SnapshotMaxPerUser 单用户快照总配额（防止存储爆炸）
	//
	// 这是**兜底**，不是重复：上面那条按 key 的保留策略明确**不碰日切基线**，
	// 而基线每天一条、按 key 攒 —— 一年一款游戏就是上千行整份存档。
	// 超出配额时删最旧的（先删基线），见 `GameSnapshotService.enforceUserQuota`。
	// 所以「日切基线永久保留」的实际含义是「保留到总配额为止」。
	SnapshotMaxPerUser = 2000
)
