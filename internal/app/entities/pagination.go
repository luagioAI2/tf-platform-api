package entities

import (
	"tf-api/internal/app/constant"

	"gorm.io/gorm"
)

// Paginator 通用分页请求/响应。
//
// 用法：
//
//	type XxxListReq struct {
//	    entities.Paginator
//	    Keyword string `json:"keyword"`
//	}
//
// 响应：{"page":1,"pageSize":30,"count":100,"list":[...]}
type Paginator struct {
	Page     int         `json:"page" form:"page" binding:"omitempty,gte=1"`
	PageSize int         `json:"pageSize" form:"pageSize" binding:"omitempty,gte=1,lte=200"`
	Count    int64       `json:"count"`
	List     interface{} `json:"list"`
}

// Normalize 补齐默认值并做上限保护
func (p *Paginator) Normalize() {
	if p.Page <= 0 {
		p.Page = constant.DEFAULT_PAGE
	}
	if p.PageSize <= 0 {
		p.PageSize = constant.DEFAULT_PAGE_SIZE
	}
	if p.PageSize > constant.MAX_PAGE_SIZE {
		p.PageSize = constant.MAX_PAGE_SIZE
	}
}

// Offset 计算偏移量
func (p *Paginator) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Paginate 执行分页查询。
// tx 需已带好 Where/Order 条件；list 传 &[]*Xxx{} 的地址。
func (p *Paginator) Paginate(tx *gorm.DB) error {
	p.Normalize()

	if err := tx.Model(p.List).Count(&p.Count).Error; err != nil {
		return err
	}
	return tx.Offset(p.Offset()).Limit(p.PageSize).Find(p.List).Error
}
