package entities

// UserSettings 家长设置的整份 JSON（每个账号一份）。
//
// ## 为什么整份存成一坨，而不是拆成列
//
// 设置里有二十多个字段，而且**每个版本都在变** —— 家长会删掉不想要的开关
// （见 `kid-quest-farm-online/src/domain/types.ts` 里那些「这里原来有 X」的注释：
// `streakBonusPerDay` / `checkInTierRatio` / `taskDropsEnabled` 都被删过）。
//
// 拆成列的话，每加一个字段要改三处：建表、DTO、映射。漏一处就是
// 「家长在界面上设了、服务端没存」—— 而且不报错，下次打开又变回去。
// 整份 JSON 存下来，服务端**不解释**它，客户端加字段不用动服务端。
//
// ## 但「不解释」不等于「什么都能塞」
//
// 见 `UserSettingsService.Save` 的校验：必须是 JSON **对象**、≤ 32 KiB。
// 否则这张表会变成一个「可以存任意文件的免费网盘」，而且没人清理。
//
// ## ⚠️ 这份东西里有 PIN
//
// `parentPin` 是 4 位**明文**。以前它只在本机（备份都要剥掉，因为备份文件
// 会发微信 / 传网盘）。现在它跟着整份设置上云 —— 这是 2026-11-03 明确的选择：
// 「所有本地存的，都要放到服务器」。代价写在这里，别让人以为是漏了：
// 服务端库里存着 4 位明文 PIN，能读到这张表的人就能开家长锁。
// 它的保护等级 = 服务端数据库的保护等级，不再是「仅本机」。
type UserSettings struct {
	BaseModel

	// UID 一个账号一份
	UID uint `gorm:"column:uid;uniqueIndex:uk_uid_settings;not null" json:"uid"`

	// Payload 设置本体（JSON 文本）
	Payload string `gorm:"column:payload;type:text;not null" json:"payload"`
	// Size 字节数（省得每次 len()，也方便发现「谁塞了 30 KB」）
	Size int `gorm:"column:size;default:0" json:"size"`
	// Version 客户端自己带的版本号（目前只用于排查「谁覆盖了谁」）
	Version int64 `gorm:"column:version;default:0" json:"version"`
}

func (UserSettings) TableName() string { return "user_settings" }

// MaxUserSettingsBytes 设置 JSON 的上限。
//
// 实际大小 ~1.5 KB（二十多个字段 + 农场时钟）。留 32 KiB 是给
// 以后加字段（比如任务模板、主题）的余量，同时也是**硬上限**：
// 超过就拒，别让它变成文件存储。
const MaxUserSettingsBytes = 32 * 1024
