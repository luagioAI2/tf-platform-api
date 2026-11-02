package entities

// ============================ 存档快照 DTO ============================

// GameSnapshotItem 快照列表项（不含 value，减少传输）
type GameSnapshotItem struct {
	ID              uint   `json:"id"`
	GameID          string `json:"game_id"`
	Key             string `json:"key"`
	Size            int64  `json:"size"`
	Version         int64  `json:"version"`
	Reason          string `json:"reason"`
	IsDailyBaseline bool   `json:"is_daily_baseline"`
	Note            string `json:"note"`
	CreatedAt       int64  `json:"created_at"`
}

// GameSnapshotListReq 查询快照列表
type GameSnapshotListReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	// Key 不传则列出该游戏所有 key 的快照
	Key   string `json:"key" binding:"omitempty,max=128"`
	Limit int    `json:"limit" binding:"omitempty,min=1,max=200"`
}

// GameSnapshotGetReq 查询快照详情
type GameSnapshotGetReq struct {
	SnapshotID uint `json:"snapshot_id" binding:"required,min=1"`
}

// GameSnapshotCreateReq 手动创建快照
type GameSnapshotCreateReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	Key    string `json:"key" binding:"required,max=128"`
	Note   string `json:"note" binding:"omitempty,max=128"`
}

// GameSnapshotRestoreReq 回档到指定快照
type GameSnapshotRestoreReq struct {
	SnapshotID uint   `json:"snapshot_id" binding:"required,min=1"`
	Reason     string `json:"reason" binding:"omitempty,max=255"`
}

// GameSnapshotRestoreByTimeReq 按时间点回档
type GameSnapshotRestoreByTimeReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	// Key 不传则回档整个游戏
	Key string `json:"key" binding:"omitempty,max=128"`
	// At 恢复到该时间点之前最近的快照（Unix 秒）
	At int64 `json:"at" binding:"required,min=1"`
	// MaxID 只在 id <= MaxID 的快照里挑（0 = 不限制）
	//
	// 为什么需要这一项：`at` 的粒度是**秒**，而同一秒里可能落了不止一份快照 ——
	// 典型场景是「界面刚把还原点列表画出来，孩子又玩了一下，服务端自己补了一份
	// 更新的快照」。这时只按 `created_at <= at` 挑，挑到的是**那一秒里最新的一份**，
	// 也就是界面没显示、家长没点的那份：预览说「退回到 21:30 那份」，
	// 实际退回到的是 21:30:00 里更晚写进去的另一个状态。
	//
	// 界面把自己看到的最大 id 带上来，服务端就只在**它看到的那批**里挑。
	// 快照 id 是自增的，所以「id <= MaxID」正好等于「界面读列表时已经存在的行」。
	// 不传（0）保持旧行为，老客户端不受影响。
	MaxID  uint   `json:"max_id" binding:"omitempty"`
	Reason string `json:"reason" binding:"omitempty,max=255"`
}

// GameSnapshotRestoreLogItem 回档日志项
type GameSnapshotRestoreLogItem struct {
	ID          uint   `json:"id"`
	GameID      string `json:"game_id"`
	Key         string `json:"key"`
	SnapshotID  uint   `json:"snapshot_id"`
	FromVersion int64  `json:"from_version"`
	ToVersion   int64  `json:"to_version"`
	OperatorID  uint   `json:"operator_id"`
	Reason      string `json:"reason"`
	CreatedAt   int64  `json:"created_at"`
}
