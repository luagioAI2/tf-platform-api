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
	At     int64  `json:"at" binding:"required,min=1"`
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
