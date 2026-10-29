package entities

// ============================ 游戏版本分发 ============================
//
// ## 为什么需要 OSS/CDN
//
// `kid-quest-farm-2d` 的 `public/games/` 实测 **1492 个文件 / 169 MB**
// （21 款游戏，单包 3~6 MB，外加贴图资源）。
//
// 这个体量**不可能走 API 进程**：
//   - 单实例带宽与连接数会被下载打满
//   - 169 MB 放进 Git 或容器镜像都不合适
//
// 所以：**包体放对象存储/CDN，客户端直连下载**，服务端只负责
//   ① 发「目录」（catalog.json + Ed25519 签名）
//   ② 发下载地址（可以是 CDN 公网地址，也可以是限时签名 URL）
//   ③ 记录版本与发布状态
//
// ## 客户端已有的校验链（不要重复实现）
//
// 前端 `src/games/install.ts` 已经定义了完整链路：
//
//	catalog.json ──┐
//	               ├─► 验签 ──► 解析 ──► 找到那一款 ──► 取包 ──► 验 SHA-256 ──► 代码文本
//	catalog.json.sig ┘
//
// 公钥**烧进 APK**。所以服务端只要产出符合该格式的 catalog + 签名即可，
// 不需要自己发明协议。
//
// ## catalog 格式（对齐 `src/games/verify.ts`）
//
//	{ "version": 1, "games": [ { id, name, version, url, sha256, size?, assetBase? } ] }
//
// `.sig` 文件是**单行 base64**。

// GameRelease 游戏版本发布记录。
type GameRelease struct {
	BaseModel

	// GameID 游戏 id（与 Game.GameID 同一套 id 空间）
	GameID string `gorm:"column:game_id;size:32;index:idx_game_channel;not null" json:"game_id"`
	// Version 语义化版本，如 1.6.2
	Version string `gorm:"column:version;size:32;not null" json:"version"`

	// Channel 发布通道：stable / beta
	Channel string `gorm:"column:channel;size:16;index:idx_game_channel;default:'stable'" json:"channel"`

	// URL 包地址（OSS/CDN 公网地址，或相对路径）
	URL string `gorm:"column:url;size:512;not null" json:"url"`
	// SHA256 包文件的 SHA-256（**小写 64 位 hex**）
	SHA256 string `gorm:"column:sha256;size:64;not null" json:"sha256"`
	// Size 字节数（下载前体积提示，**不是校验依据**）
	Size int64 `gorm:"column:size;default:0" json:"size"`

	// AssetBase 资源根（以 `/` 结尾）。空 = 包所在目录
	//
	// 包本体可能放 GitHub Release（体积小、更新频繁），
	// 而几十 MB 的贴图放在 CDN。两者不在同一个目录，
	// 所以「资源根 = 包地址的目录」不能当默认假设硬编码。
	AssetBase string `gorm:"column:asset_base;size:512" json:"asset_base"`

	// MinHostApi 要求的最低宿主 API 版本
	MinHostApi int `gorm:"column:min_host_api;default:1" json:"min_host_api"`

	// Active 是否为「当前生效版本」
	//
	// 同一 (game_id, channel) 只应有一条 active = true。
	// 发布新版本时把旧的置 false（见 GameReleaseRepository.Publish）。
	Active bool `gorm:"column:active;default:false;index" json:"active"`

	// Forced 是否强制更新（客户端必须升级才能继续玩）
	Forced bool `gorm:"column:forced;default:false" json:"forced"`

	// ReleaseNotes 更新说明
	ReleaseNotes string `gorm:"column:release_notes;size:1024" json:"release_notes"`

	// DownloadCount 下载次数（粗略统计，异步累加）
	DownloadCount int64 `gorm:"column:download_count;default:0" json:"download_count"`

	// PublishedAt 发布时间
	PublishedAt int64 `gorm:"column:published_at;default:0;index" json:"published_at"`

	// PublishedBy 发布人（管理员 ID）
	PublishedBy uint `gorm:"column:published_by;default:0" json:"published_by"`
}

func (GameRelease) TableName() string { return "game_release" }

// 发布通道
const (
	ReleaseChannelStable = "stable"
	ReleaseChannelBeta   = "beta"
)

// GameCatalog catalog.json 的结构（与前端 `verify.ts` 的 `Catalog` 一致）。
//
// ⚠️ 字段顺序和名字**不能改** —— 前端会验签后直接解析这个 JSON。
// 改任何一个字段名都要同步改前端 `verify.ts` 并重新发版公钥链路。
type GameCatalog struct {
	// Version catalog 格式版本（当前 1）
	Version int `json:"version"`
	// Games 游戏列表
	Games []GameCatalogEntry `json:"games"`
}

// GameCatalogEntry catalog 里的一条（与前端 `CatalogGame` 一致）。
type GameCatalogEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	// SHA256 小写 64 位 hex
	SHA256 string `json:"sha256"`
	// Size 可选
	Size int64 `json:"size,omitempty"`
	// AssetBase 可选，以 `/` 结尾
	AssetBase string `json:"assetBase,omitempty"`
}

// CatalogFormatVersion 当前 catalog 格式版本
const CatalogFormatVersion = 1

// ============================ 解锁来源 ============================

// 游戏解锁来源（追加到 UserGame.Source）
const (
	// GameOwnedByWish 用心愿商城的券兑换（gamebox）
	GameOwnedByWish = "wish"
	// GameOwnedByBlindbox 盲盒开出
	GameOwnedByBlindbox = "blindbox"
	// GameOwnedByCode 兑换码解锁
	GameOwnedByCode = "code"
)

// ============================ 游戏包校验常量 ============================

const (
	// GamePackageMaxBytes 单包体积上限（64MB）
	//
	// 实测最大包 5.9 MB（melonjar），留 10 倍余量。
	// 这是**上传时**的守卫，不是客户端下载限制。
	GamePackageMaxBytes = 64 << 20
	// GameAssetBaseSuffix 资源根必须以 `/` 结尾
	GameAssetBaseSuffix = "/"
)
