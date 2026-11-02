package entities

// ============================ 兑换商城 DTO ============================

// CreateRedeemItemReq 创建兑换商品
type CreateRedeemItemReq struct {
	// ItemID 自定义业务 ID（不传则自动生成）
	ItemID string `json:"id" binding:"omitempty,max=64"`

	Name     string         `json:"name" binding:"required,max=64"`
	Emoji    string         `json:"emoji" binding:"omitempty,max=16"`
	Category RedeemCategory `json:"category" binding:"required,oneof=snack gift screen outing privilege custom"`
	// Price 所需「分」（1 券 = 1 分）
	Price int64  `json:"cost" binding:"required,min=1,max=100000"`
	Note  string `json:"note" binding:"omitempty,max=255"`

	// LimitPerDay 每天最多换几个（0 = 不限）
	LimitPerDay int `json:"limit_per_day" binding:"omitempty,min=0,max=100"`
	// Once 一次性（永不重置）
	Once bool `json:"once"`
	// Stock 库存。-1 = 无限；0 会被归一为 -1
	Stock int `json:"stock" binding:"omitempty,min=-1,max=100000"`
	Sort  int `json:"sort"`
}

// UpdateRedeemItemReq 更新兑换商品（全字段可选）
type UpdateRedeemItemReq struct {
	Name        *string         `json:"name" binding:"omitempty,max=64"`
	Emoji       *string         `json:"emoji" binding:"omitempty,max=16"`
	Category    *RedeemCategory `json:"category" binding:"omitempty"`
	Price       *int64          `json:"cost" binding:"omitempty,min=1,max=100000"`
	Note        *string         `json:"note" binding:"omitempty,max=255"`
	LimitPerDay *int            `json:"limit_per_day" binding:"omitempty,min=0,max=100"`
	Once        *bool           `json:"once"`
	Stock       *int            `json:"stock" binding:"omitempty,min=-1,max=100000"`
	Archived    *bool           `json:"archived"`
	Sort        *int            `json:"sort"`
}

// ListRedeemItemReq 查询兑换商品
type ListRedeemItemReq struct {
	// IncludeArchived 是否包含已下架（家长端用）
	IncludeArchived bool `json:"include_archived"`

	// Raw 不做「换过 / 缺货」过滤，把商品原样全给出来。
	//
	// 用途只有一个：**客户端把商城镜像到本机**（`kid-quest-farm-online`
	// 的在线版，家长管理页要看到「一次性 · 已换过」的那件东西）。
	//
	// ⚠️ 默认的过滤是**给孩子看的口径**，缺了它家长会以为商品被删了：
	// 一次性商品换过之后，默认列表里就再也找不到它，而兑换记录还在 ——
	// 家长的下一个动作通常是「重新建一个」，于是同一件东西建了两条。
	//
	// Raw = true 时每行带 `visible`（现在该不该出现在孩子的列表里），
	// 由客户端自己决定怎么展示，服务端不再替它删行。
	Raw bool `json:"raw"`
}

// RedeemItemView 商品视图（附带可换性判断）
type RedeemItemView struct {
	RedeemItem

	// TodayCount 今天已换数量
	TodayCount int64 `json:"today_count"`
	// RemainingToday 今天还可换几个（-1 = 不限）
	RemainingToday int `json:"remaining_today"`
	// Plan 按当前余额算出的分账预览
	Plan RedeemPayment `json:"plan"`
	// CanRedeem 现在能不能换
	CanRedeem bool `json:"can_redeem"`
	// BlockedByLimit 是否因为每日限额而被挡住
	BlockedByLimit bool `json:"blocked_by_limit"`
	// Description 「30 兑换券 + 20 积分」这类可读文案
	Description string `json:"description"`
}

// RedeemReq 兑换请求
type RedeemReq struct {
	ItemID string `json:"item_id" binding:"required,max=64"`
	// Quantity 换几个（默认 1）
	Quantity int `json:"quantity" binding:"omitempty,min=1,max=50"`
	// ClientRequestID 幂等键（网络重试时避免重复扣款）
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// RedeemResultResp 兑换结果
type RedeemResultResp struct {
	RecordID string `json:"record_id"`
	ItemID   string `json:"item_id"`
	Name     string `json:"name"`
	Emoji    string `json:"emoji"`
	Quantity int    `json:"quantity"`
	// Plan 实际分账
	Plan RedeemPayment `json:"plan"`
	// Description 可读文案
	Description string `json:"description"`
	// Wallet 兑换后的余额
	Wallet WalletInfo `json:"wallet"`
}

// RedeemRecordView 兑换记录视图
type RedeemRecordView struct {
	RedeemRecord
	// Description 「30 兑换券 + 20 积分」这类可读文案
	Description string `json:"description"`
}

// RedeemHandleReq 兑现/取消
type RedeemHandleReq struct {
	RecordID string `json:"record_id" binding:"required,max=64"`
	// Note 兑现备注（fulfill 时用）
	Note string `json:"note" binding:"omitempty,max=255"`
	// Reason 取消原因（cancel 时用，对孩子可见）
	Reason string `json:"reason" binding:"omitempty,max=255"`
}

// ListRedeemRecordReq 查询兑换记录
type ListRedeemRecordReq struct {
	Paginator
}

// RedeemStatsReq 兑换统计
type RedeemStatsReq struct {
	Days int `json:"days" binding:"omitempty,min=1,max=365"`
}

// ============================ 心愿商城 DTO ============================

// ListWishItemReq 查询心愿商品
type ListWishItemReq struct {
	// Kind 分类筛选（空 = 全部）
	Kind WishKind `json:"kind" binding:"omitempty"`
	// IncludeArchived 是否包含已下架
	IncludeArchived bool `json:"include_archived"`
}

// WishItemView 心愿商品视图（对孩子）
//
// ⚠️ **不含 `BuyURL` / `BuyLabel`** —— 外链只在家长确认页出现，孩子端读不到。
type WishItemView struct {
	WishItem
	// Open 本家庭是否已开放（家长定过价）
	Open bool `json:"open"`
	// VoucherPrice 券价（Open 为 false 时是 0）
	VoucherPrice int64 `json:"voucher_price"`
	// Affordable 券够不够
	Affordable bool `json:"affordable"`
	// Shortfall 还差多少券
	Shortfall int64 `json:"shortfall"`
	// HasOrder 是否已有进行中的订单
	HasOrder bool `json:"has_order"`
	// OrderStatus 进行中订单的状态
	OrderStatus WishStatus `json:"order_status,omitempty"`
	// Owned 游戏类商品：是否已拥有（已拥有就不列出）
	Owned bool `json:"owned"`
	// MyWish 孩子是否已表明意向
	MyWish bool `json:"my_wish"`
	// LastRejectReason 上次被回绝的原因（「上次爸妈说：…」）
	LastRejectReason string `json:"last_reject_reason,omitempty"`
	// DaysToSave 大约要攒几天（-1 = 算不出）
	DaysToSave int `json:"days_to_save"`
	// BlindBoxRemaining 盲盒池里还剩几款没拥有（只对 blindbox 有意义）
	BlindBoxRemaining int `json:"blind_box_remaining"`
}

// WishItemDetailResp 心愿商品详情（家长端，含外链）
type WishItemDetailResp struct {
	WishItemView
	// BuyURL 外链（**仅家长端返回**）
	BuyURL string `json:"buy_url,omitempty"`
	// BuyLabel 外链按钮文字
	BuyLabel string `json:"buy_label,omitempty"`
}

// WishReq 孩子表明意向（「想要这个」）
type WishReq struct {
	ItemID string `json:"item_id" binding:"required,max=64"`
}

// WishRedeemReq 确定兑换（扣券）
type WishRedeemReq struct {
	ItemID string `json:"item_id" binding:"required,max=64"`
	// Ship 收货信息（实物商品必填）
	Ship *WishShipInfo `json:"ship"`
	// ClientRequestID 幂等键
	ClientRequestID string `json:"client_request_id" binding:"omitempty,max=64"`
}

// WishHandleReq 推进订单状态
type WishHandleReq struct {
	OrderID string `json:"order_id" binding:"required,max=64"`
	// Reason 取消原因（对孩子可见）
	Reason string `json:"reason" binding:"omitempty,max=255"`
	// TrackingNo 快递单号（done 时可填）
	TrackingNo string `json:"tracking_no" binding:"omitempty,max=64"`
}

// WishSetOfferReq 家长定价/开放
type WishSetOfferReq struct {
	ItemID string `json:"item_id" binding:"required,max=64"`
	// VoucherPrice 券价（0 = 关闭该商品）
	VoucherPrice int64 `json:"voucher_price" binding:"omitempty,min=0,max=100000"`
	// DiamondPrice 钻石价（0 = 不卖）
	DiamondPrice int64 `json:"diamond_price" binding:"omitempty,min=0,max=100000"`
}

// WishOrderView 心愿单视图
type WishOrderView struct {
	WishOrder
	// StatusText 状态中文说法
	StatusText string `json:"status_text"`
	// Digital 是否数字商品（兑换当场交付）
	//
	// ⚠️ 判据是 `OpenedGameID != ""`，**不是** kind：
	// 盒子装包失败会走取消退回，那种单子本来就不该显示成「已交付」。
	Digital bool `json:"digital"`
	// GameName 开出的游戏名（数字商品才有）
	GameName string `json:"game_name,omitempty"`
	// BuyURL 外链（**仅家长端**）
	BuyURL string `json:"buy_url,omitempty"`
	// BuyLabel 外链按钮文字
	BuyLabel string `json:"buy_label,omitempty"`
}

// WishCodeRedeemReq 兑换码解锁
type WishCodeRedeemReq struct {
	// Code 明文兑换码（服务端算 hash 后比对）
	Code string `json:"code" binding:"required,min=4,max=64"`
}

// WishCodeResp 兑换码结果
type WishCodeResp struct {
	Code     string `json:"code"`
	ItemID   string `json:"item_id"`
	Unlocked bool   `json:"unlocked"`
	GameID   string `json:"game_id,omitempty"`
	Vouchers int64  `json:"vouchers,omitempty"`
}

// ListWishOrderReq 查询心愿单
type ListWishOrderReq struct {
	Paginator
	Status WishStatus `json:"status" binding:"omitempty"`
}

// ============================ 游戏分发 DTO ============================

// PublishReleaseReq 发布游戏版本
type PublishReleaseReq struct {
	GameID  string `json:"game_id" binding:"required,max=32"`
	Version string `json:"version" binding:"required,max=32"`
	Channel string `json:"channel" binding:"omitempty,oneof=stable beta"`

	// URL 包地址（OSS/CDN 公网地址）
	URL string `json:"url" binding:"required,max=512"`
	// SHA256 包文件哈希（64 位小写 hex）
	SHA256 string `json:"sha256" binding:"required,len=64"`
	// Size 字节数
	Size int64 `json:"size" binding:"omitempty,min=0"`
	// AssetBase 资源根（以 / 结尾）
	AssetBase string `json:"asset_base" binding:"omitempty,max=512"`

	MinHostApi int `json:"min_host_api" binding:"omitempty,min=1,max=100"`
	// Forced 是否强制更新
	Forced bool `json:"forced"`
	// ReleaseNotes 更新说明
	ReleaseNotes string `json:"release_notes" binding:"omitempty,max=1024"`
}

// ListReleaseReq 查询版本列表
type ListReleaseReq struct {
	GameID  string `json:"game_id" binding:"omitempty,max=32"`
	Channel string `json:"channel" binding:"omitempty,oneof=stable beta"`
	// OnlyActive 只看当前生效版本
	OnlyActive bool `json:"only_active"`
}

// GameReleaseView 版本视图
type GameReleaseView struct {
	GameRelease
	// GameName 游戏名
	GameName string `json:"game_name,omitempty"`
	// UpdateAvailable 相对客户端上报的版本，是否有更新
	UpdateAvailable bool `json:"update_available,omitempty"`
}

// GameUpdateCheckReq 客户端检查更新
type GameUpdateCheckReq struct {
	// Installed 已安装的 game_id → version
	Installed map[string]string `json:"installed"`
	Channel   string            `json:"channel" binding:"omitempty,oneof=stable beta"`
}

// GameUpdateItem 单个游戏的更新信息
type GameUpdateItem struct {
	GameID          string `json:"game_id"`
	Name            string `json:"name"`
	InstalledVer    string `json:"installed_version"`
	LatestVer       string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	Forced          bool   `json:"forced"`
	Size            int64  `json:"size"`
	SHA256          string `json:"sha256"`
	URL             string `json:"url"`
	AssetBase       string `json:"asset_base"`
	MinHostApi      int    `json:"min_host_api"`
	ReleaseNotes    string `json:"release_notes"`
}

// GameUpdateCheckResp 更新检查结果
type GameUpdateCheckResp struct {
	// CatalogURL 目录地址（客户端可自行拉取完整目录）
	CatalogURL string `json:"catalog_url"`
	// CatalogSigURL 目录签名地址
	CatalogSigURL string `json:"catalog_sig_url"`
	// Updates 有更新的游戏
	Updates []*GameUpdateItem `json:"updates"`
	// All 全部游戏的当前版本（便于客户端比对）
	All []*GameUpdateItem `json:"all"`
}

// GameDownloadReq 获取下载地址
type GameDownloadReq struct {
	GameID string `json:"game_id" binding:"required,max=32"`
	// Channel 通道
	Channel string `json:"channel" binding:"omitempty,oneof=stable beta"`
	// Version 指定版本（空 = 当前生效版本）
	Version string `json:"version" binding:"omitempty,max=32"`
}

// GameDownloadResp 下载信息
type GameDownloadResp struct {
	GameID    string `json:"game_id"`
	GameName  string `json:"name,omitempty"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
	AssetBase string `json:"asset_base,omitempty"`
	// ExpiresIn 签名 URL 有效期（秒）。0 = 长期有效（CDN 公开地址）
	ExpiresIn int `json:"expires_in"`
}
