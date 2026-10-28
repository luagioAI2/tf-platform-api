package entities

import (
	"time"

	"gorm.io/gorm"
)

// BaseModel 所有持久化模型的内嵌基类。
//
// 约定：
//   - 主键 uint 自增
//   - 时间统一用 int64（Unix 秒），便于跨语言/跨时区处理
//   - 软删除用 DeletedAt（查询自动过滤）
type BaseModel struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt int64          `gorm:"autoCreateTime:false" json:"created_at"`
	UpdatedAt int64          `gorm:"autoUpdateTime:false" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// BeforeCreate 创建前填充时间戳
func (b *BaseModel) BeforeCreate(tx *gorm.DB) error {
	now := time.Now().Unix()
	if b.CreatedAt == 0 {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	return nil
}

// BeforeUpdate 更新前刷新时间戳
func (b *BaseModel) BeforeUpdate(tx *gorm.DB) error {
	b.UpdatedAt = time.Now().Unix()
	return nil
}
