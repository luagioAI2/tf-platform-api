package entities

// ============================ 招商申请 DTO ============================

// MerchantApplyReq 提交/更新招商申请。
//
// ⚠️ **同一角色重复提交 = 改申请**（不是提第二份）——
// 客户端原话：「同一个角色重复提交 = 改申请，不是提第二份，后端那边也要按这个语义」。
// 所以这个接口是**幂等**的：同一个 uid + role 永远只有一条记录。
type MerchantApplyReq struct {
	Role MerchantRole `json:"role" binding:"required,oneof=parent merchant"`
	// Name 家长昵称 / 店铺名。
	//
	// ⚠️ **可以不传**（2026-10-29 上游改口）—— 客户端原话：
	// 「点申请即可。不需要填 称呼 这些。**因为会根据当前账号，申请**。」
	// 不传就由服务端从账号里取（昵称 → 手机号）。
	Name string `json:"name" binding:"omitempty,max=64"`
	// Contact 手机号或微信。同上：不传就用账号自己的手机号。
	//
	// ⚠️ 客户端拿不到明文手机号（登录响应里 `mobile` 是 `138****8000`），
	// 所以**只能由服务端填** —— 别指望客户端补这个字段。
	Contact string `json:"contact" binding:"omitempty,max=64"`
	// Note 一句话介绍
	Note string `json:"note" binding:"omitempty,max=255"`
}

// MerchantMyResp 我的申请（客户端读这个）
//
// ⚠️ 客户端**只渲染 `approved` 的 `admin_url`**，`pending` 时那个区域是
// 「审核通过后这里会出现链接」。所以服务端不要把 `admin_url` 在
// 非 approved 时返回 —— 别让客户端去判断。
type MerchantMyResp struct {
	Applications []*MerchantApplicationView `json:"applications"`
}

// MerchantApplicationView 申请单视图（对客户端）
type MerchantApplicationView struct {
	// ID 申请单 ID。
	//
	// ⚠️ **后台审核时要用它**（`review` 的 `application_id`）——
	// 一开始漏了这个字段，后台列表里根本拿不到能点「通过」的东西。
	// 客户端不看它，但返回去也无害。
	ID     uint           `json:"id"`
	Role   MerchantRole   `json:"role"`
	Status MerchantStatus `json:"status"`
	// StatusText 状态中文（客户端暂未用，后台列表用）
	StatusText string `json:"status_text"`
	Name       string `json:"name"`
	Contact    string `json:"contact"`
	Note       string `json:"note"`
	// AdminURL **只有 approved 时才有值**（服务端负责抑制）
	AdminURL string `json:"admin_url,omitempty"`
	// CreatedAt 提交时间
	CreatedAt int64 `json:"created_at"`
	// ReviewedAt 审核时间（0 = 未审）
	ReviewedAt int64 `json:"reviewed_at"`
}

// ============================ 后台侧 ============================

// MerchantListReq 后台申请列表
type MerchantListReq struct {
	Paginator
	Role   MerchantRole   `json:"role" binding:"omitempty"`
	Status MerchantStatus `json:"status" binding:"omitempty"`
	// Keyword 按名字/联系方式模糊搜
	Keyword string `json:"keyword" binding:"omitempty,max=64"`
}

// MerchantReviewReq 后台审核。
//
// `AdminURL` 由**后台填**，不是客户端 —— 客户端拼出来的地址一旦猜错，
// 用户点进去是 404，而界面上写着「已通过审核」。
type MerchantReviewReq struct {
	ApplicationID uint `json:"application_id" binding:"required,min=1"`
	// Approve true = 通过；false = 拒绝
	Approve bool `json:"approve"`
	// AdminURL 通过时给对方的后台地址（**必填** —— 通过了却不给地址，
	// 客户端那一屏会永远停在「审核通过后这里会出现链接」）
	AdminURL string `json:"admin_url" binding:"omitempty,max=512"`
	// Note 审核备注（拒绝原因）
	Note string `json:"note" binding:"omitempty,max=255"`
}
