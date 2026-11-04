package entities

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// ============================ 任务 / 兑换模板 ============================
//
// ## 为什么要有「模板」这一层
//
// App 首启时把 `SEED_TASKS` / `SEED_REDEEM_ITEMS` 写进本地 Dexie，
// 那是**硬编码在客户端**的。运营想改一条默认任务、或商家想给自己的
// 推广用户换一套任务，只能发版 —— 这就是这一层要解决的事。
//
// 模板是**下发用的蓝图**，不是用户的任务表：
//   - `task` / `redeem_item` 是每用户一份的**实例**（`uid` 列）；
//   - `template` / `template_item` 是**没有 uid** 的公共定义。
// 客户端拿到模板后才在本地铺出实例，所以「改模板」对已经装过的老用户
// 不生效（这点必须说清楚，别让人以为改了后台老用户立刻变）。
//
// ## 归属与默认（用户选的口径：「选一个默认模板」，不是叠加）
//
//   - 官方模板：`scope='official'` + `owner_uid=0`，**兜底**；
//   - 商家模板：`scope='merchant'` + `owner_uid=<商家 App uid>`；
//   - 用户身上有 `source_merchant_uid`（注册带邀请码时写入）。
//
// 解析顺序：先找「该商家 + 该 kind」的默认模板，没有就回落官方默认。
// 商家「新增了模板并设为默认」就用新模板，否则官方模板就是默认。

// TemplateKind 模板种类
type TemplateKind string

const (
	TemplateKindTask   TemplateKind = "task"
	TemplateKindRedeem TemplateKind = "redeem"
)

// IsValid 种类是否合法
func (k TemplateKind) IsValid() bool {
	return k == TemplateKindTask || k == TemplateKindRedeem
}

// TemplateScope 模板归属范围
type TemplateScope string

const (
	TemplateScopeOfficial TemplateScope = "official"
	TemplateScopeMerchant TemplateScope = "merchant"
)

// IsValid 范围是否合法
func (s TemplateScope) IsValid() bool {
	return s == TemplateScopeOfficial || s == TemplateScopeMerchant
}

// Template 模板头。
//
// ⚠️ `created_at` / `updated_at` 是 int64 且 `autoCreateTime:false`
// —— GORM **不会**自动填，每个写入点都要手写，否则落库是 0（1970 年）。
type Template struct {
	BaseModel

	Name string       `gorm:"column:name;size:64;not null" json:"name"`
	Kind TemplateKind `gorm:"column:kind;size:16;index;not null" json:"kind"`

	// Scope / OwnerUID 一起决定「这份模板归谁」。
	// ★ owner_uid = 0 且 scope=official 才是官方；0 不是「没填」的占位符。
	Scope    TemplateScope `gorm:"column:scope;size:16;index;not null" json:"scope"`
	OwnerUID uint          `gorm:"column:owner_uid;index;default:0;not null" json:"owner_uid"`

	// IsDefault 同一 (kind, scope, owner_uid) 分组里只能有一份为 1，
	// 由 service 层在「设为默认」时先清掉其它行，不靠数据库约束。
	IsDefault bool   `gorm:"column:is_default;index;default:false" json:"is_default"`
	Note      string `gorm:"column:note;size:255" json:"note"`
	Sort      int    `gorm:"column:sort;default:0" json:"sort"`

	// Source 标出这份模板是种子写入还是后台手建的。
	// 种子只在「该 kind 没有任何官方模板」时写入 —— 运营一旦改过官方模板，
	// 重启服务**不许**把它覆盖回去。
	Source string `gorm:"column:source;size:16;default:'manual'" json:"source"`
}

func (Template) TableName() string { return "template" }

// TemplateItem 模板条目。
//
// 两种 kind 共用一张表：任务用 cycle / planned_minutes / base_points，
// 兑换用 price / limit_per_day / stock / once，另一半字段留零值。
// ★ 索引名必须**全局唯一**（SQLite 的索引名是整个库共享的）。
//
// Extra 存「服务端不解释、客户端要保留」的玩法参数（JSON，camelCase）：
// 签到开关、阶梯倍率、奖励道具、提醒时间、createdByParent …
// 和 `Task.Extra` 是同一个思路：加一个玩法参数不用改表。
type TemplateItem struct {
	BaseModel

	TemplateID uint   `gorm:"column:template_id;uniqueIndex:uk_tpl_item;index;not null" json:"template_id"`
	ItemID     string `gorm:"column:item_id;size:64;uniqueIndex:uk_tpl_item;not null" json:"item_id"`

	Name           string `gorm:"column:name;size:64;not null" json:"name"`
	Emoji          string `gorm:"column:emoji;size:16" json:"emoji"`
	Category       string `gorm:"column:category;size:16" json:"category"`
	Cycle          string `gorm:"column:cycle;size:16" json:"cycle"`
	PlannedMinutes int    `gorm:"column:planned_minutes;default:30" json:"planned_minutes"`
	BasePoints     int    `gorm:"column:base_points;default:10" json:"base_points"`
	Price          int64  `gorm:"column:price;default:0" json:"price"`
	Note           string `gorm:"column:note;size:255" json:"note"`
	LimitPerDay    int    `gorm:"column:limit_per_day;default:0" json:"limit_per_day"`
	Stock          int    `gorm:"column:stock;default:-1" json:"stock"`
	Once           bool   `gorm:"column:once;default:false" json:"once"`
	Sort           int    `gorm:"column:sort;default:0" json:"sort"`
	Extra          string `gorm:"column:extra;type:text" json:"extra"`
}

func (TemplateItem) TableName() string { return "template_item" }

// ============================ 请求 / 响应 ============================

// ResolveTemplateReq 取模板。`kind` 必填 —— 一次只下发一种，
// 免得客户端拿到 16 条任务和 15 条兑换品还得自己分。
type ResolveTemplateReq struct {
	Kind TemplateKind `json:"kind" binding:"required,oneof=task redeem"`
}

// BindInviteReq 注册 / 绑定邀请码。
type BindInviteReq struct {
	InviteCode string `json:"invite_code" binding:"required,max=32"`
}

// TemplateResolveResp 下发结果。
//
// Items 里的每条是「typed 列（snake_case）+ Extra 里的原生字段（camelCase）」
// 合并出来的 map：客户端按 snake_case 读列、按 camelCase 读玩法参数，
// 两边键名不冲突，所以运营在后台改了 typed 列不会被 Extra 里的旧值盖掉。
type TemplateResolveResp struct {
	TemplateID       uint                     `json:"template_id"`
	Name             string                   `json:"name"`
	Kind             string                   `json:"kind"`
	Scope            string                   `json:"scope"`
	OwnerUID         uint                     `json:"owner_uid"`
	IsDefault        bool                     `json:"is_default"`
	SourceMerchantUID uint                    `json:"source_merchant_uid"`
	Items            []map[string]interface{} `json:"items"`
}

// ============================ 邀请码 ============================
//
// 邀请码是**由商家 uid 确定性推导**出来的，不落表：
//   `TF<uid 的 36 进制>-<4 位校验>`
//
// 为什么不用「随机码 + 一张邀请码表」：商家后台要能立刻看到自己的码、
// 运营要能在招商列表里看到每个商家的码，而这两个界面都不该为了显示
// 一个字符串再联一张表。校验位只是防止手抄错 / 随手瞎填，
// 真正的准入闸门在 `ApplyInvite`：uid 必须是**已通过审核的商家**。

const invitePrefix = "TF"

// MakeInviteCode 由商家 uid 生成邀请码。uid=0 返回空串。
func MakeInviteCode(uid uint) string {
	if uid == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("tf-invite-v1:%d", uid)))
	return invitePrefix + strings.ToUpper(strconv.FormatUint(uint64(uid), 36)) +
		"-" + strings.ToUpper(hex.EncodeToString(h[:2]))
}

// ParseInviteCode 解析并校验邀请码，返回商家 uid。
func ParseInviteCode(code string) (uint, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	parts := strings.Split(code, "-")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], invitePrefix) {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(parts[0], invitePrefix), 36, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	uid := uint(n)
	if uint64(uid) != n {
		return 0, false
	}
	// 校验不过必须连 uid 一起归零：调用方习惯只判 ok，
	// 留一个「解析出来但没验过」的 uid 迟早被拿去当商家归属。
	if !strings.EqualFold(code, MakeInviteCode(uid)) {
		return 0, false
	}
	return uid, true
}
