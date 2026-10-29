package entities

// ============================ 招商申请 ============================
//
// 上游 `kid-quest-farm-2d` 在 2026-10-29 加了这一屏（`MerchantView.tsx`），
// 并在类型注释里**明确把后端那一半留出来**：
//
//	「两路人马共用一套申请单：
//	   · `parent`   —— 家长邀请商家入驻，**拿商家收益的分成**
//	   · `merchant` —— 商家自己把东西挂进心愿商城
//
//	 ⚠️ 现在**没有后端**，所以这里只落「谁、什么时候、申请了什么」，
//	    审核结果和后台地址都是**留给后端回填**的字段（见 `adminUrl`）。
//	    别在客户端自己把状态推成 `approved` —— 那等于给所有用户
//	    凭空发一个后台入口。」
//
// 所以服务端要做的就是那两件客户端**明确不自己造**的事：
//
//  1. **审核**（`pending` → `approved`）
//  2. **回填后台地址**（`AdminURL`）—— 只有审核通过才有值
//
// 客户端的「提交」语义（原话）：**同一个角色重复提交 = 改申请，不是提第二份**。
// 服务端对齐这条：唯一索引建在 `(uid, role)` 上，不是 `(uid, role, 提交次数)`。

// MerchantRole 申请人是家长还是商家
type MerchantRole string

const (
	// MerchantRoleParent 家长邀请商家入驻（**拿商家收益的分成**）
	MerchantRoleParent MerchantRole = "parent"
	// MerchantRoleMerchant 商家自己把东西挂进心愿商城
	MerchantRoleMerchant MerchantRole = "merchant"
)

// IsValid 角色是否合法
func (r MerchantRole) IsValid() bool {
	return r == MerchantRoleParent || r == MerchantRoleMerchant
}

// MerchantStatus 审核状态。
//
// 三档：待审 / 通过 / 拒绝。
//
// ⚠️ `rejected` 一开始客户端渲染不出来（那一版只认 pending / approved），
// 2026-10-29 晚客户端补上了这一档 —— 因为「后台总得能拒」，
// 而拒了之后如果客户端还显示「审核中」，被拒的人会一直等。
// 现在两边一致，别再按「已知的不一致」处理。
type MerchantStatus string

const (
	// MerchantStatusPending 已提交，等后台审核
	MerchantStatusPending MerchantStatus = "pending"
	// MerchantStatusApproved 审核通过 —— **此时 AdminURL 才可能有值**
	MerchantStatusApproved MerchantStatus = "approved"
	// MerchantStatusRejected 已拒绝（服务端有，客户端暂不渲染）
	MerchantStatusRejected MerchantStatus = "rejected"
)

// IsValid 状态是否合法
func (s MerchantStatus) IsValid() bool {
	switch s {
	case MerchantStatusPending, MerchantStatusApproved, MerchantStatusRejected:
		return true
	}
	return false
}

// MerchantApplication 招商申请单。
//
// ⚠️ **唯一索引建在 `(uid, role)`**：同一角色重复提交是**改申请**，
// 不是提第二份（客户端原话：「同一个角色重复提交 = 改申请，不是提第二份
// —— 后端那边也要按这个语义」）。
type MerchantApplication struct {
	BaseModel

	UID uint `gorm:"column:uid;uniqueIndex:uk_merchant_uid_role;index;not null" json:"uid"`
	// Role 申请角色（parent / merchant）
	Role MerchantRole `gorm:"column:role;size:16;uniqueIndex:uk_merchant_uid_role;not null" json:"role"`

	// Status 审核状态
	Status MerchantStatus `gorm:"column:status;size:16;default:'pending';index" json:"status"`

	// Name 家长昵称 / 店铺名。列表里就显示这个，不再回查用户资料
	Name string `gorm:"column:name;size:64;not null" json:"name"`
	// Contact 手机号或微信，后端联系用
	Contact string `gorm:"column:contact;size:64;not null" json:"contact"`
	// Note 一句话介绍（商家：卖什么；家长：想邀请哪家店）
	Note string `gorm:"column:note;size:255" json:"note"`

	// AdminURL **后台地址**。只有审核通过时才有值。
	//
	// ⚠️ **只能由服务端在审核通过时写**。客户端不许自己拼 ——
	// 拼出来的地址一旦猜错，用户点进去是 404，而界面上写着「已通过审核」，
	// 看着像我们把他账号弄丢了。
	AdminURL string `gorm:"column:admin_url;size:512" json:"admin_url"`

	// ReviewedAt 审核时间戳（0 = 还没审）
	ReviewedAt int64 `gorm:"column:reviewed_at;default:0" json:"reviewed_at"`
	// ReviewedBy 审核人（后台管理员 ID）
	ReviewedBy uint `gorm:"column:reviewed_by;default:0" json:"reviewed_by"`
	// ReviewNote 审核备注（拒绝原因等）
	ReviewNote string `gorm:"column:review_note;size:255" json:"review_note"`
}

func (MerchantApplication) TableName() string { return "merchant_application" }

// MerchantStatusText 状态的中文说法（后台列表用）
var MerchantStatusText = map[MerchantStatus]string{
	MerchantStatusPending:  "待审核",
	MerchantStatusApproved: "已通过",
	MerchantStatusRejected: "已拒绝",
}
