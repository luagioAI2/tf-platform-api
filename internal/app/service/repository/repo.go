package repository

import (
	"tf-api/internal/app/entities"

	"gorm.io/gorm"
)

// Models 返回需要 AutoMigrate 的模型列表。
// 生产环境请关闭 EnableAutoMigrate，改用 migrations/ 下的版本化 SQL。
func Models() []interface{} {
	return []interface{}{
		// 用户与认证
		new(entities.User),
		new(entities.UserOAuth),
		new(entities.UserToken),
		new(entities.SmsCode),

		// 钱包与账本
		new(entities.Wallet),
		new(entities.LedgerEntry),

		// 家长设置（整份 JSON，一账号一行）
		new(entities.UserSettings),

		// 游戏与购买
		new(entities.Game),
		new(entities.UserGame),
		new(entities.GameOrder),

		// 游戏通用存储（存档 KV + 游戏币）
		new(entities.GameKv),
		new(entities.GameCoinLedger),
		new(entities.GameWallet),

		// 存档快照与回档
		new(entities.GameKvSnapshot),
		new(entities.GameKvRestoreLog),

		// 任务系统
		new(entities.Task),
		new(entities.TaskInstance),
		new(entities.CheckInRecord),
		new(entities.CheckInProgress),

		// 农场（整块 JSON + 乐观锁）
		new(entities.FarmState),

		// 兑换商城
		new(entities.RedeemItem),
		new(entities.RedeemRecord),

		// 心愿商城
		new(entities.WishItem),
		new(entities.WishOffer),
		new(entities.WishOrder),
		new(entities.WishCode),

		// 游戏版本分发
		new(entities.GameRelease),

		// 招商
		new(entities.MerchantApplication),
	}
}

// AutoMigrate 执行建表/改表（仅开发与测试环境使用）
func AutoMigrate(db *gorm.DB) error {
	models := Models()
	if len(models) == 0 {
		return nil
	}
	return db.AutoMigrate(models...)
}
