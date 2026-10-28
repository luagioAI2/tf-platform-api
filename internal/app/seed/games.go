package seed

import (
	"context"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"go.uber.org/zap"
)

// Games 游戏种子数据。
//
// 数据来源：kid-quest-farm-2d 的
//   - src/games/registry.ts（GAMES：id/name/emoji/tagline/kind/priceMall/defaultOwned）
//   - src/games/storefront.ts（STOREFRONT：tint/pitch/learn/age/session/shots/about）
//
// 定价说明（与原项目一致）：
//   - 农场（cloudfarm）：首款免费、默认拥有（pricePoints=0, defaultOwned=true）
//   - 抓奖乐园（prizepark）：需购买，用积分（原项目用"钻石"，这里换算为积分）
var Games = []*entities.Game{
	{
		GameID:       "cloudfarm",
		Name:         "云边小农场",
		Emoji:        "🌾",
		Tagline:      "种菜、养动物、把收成拿到市场上卖掉。",
		Kind:         "think",
		Tint:         "grass",
		Status:       entities.GameStatusAvailable,
		PricePoints:  0,
		PriceDiamond: 0,
		DefaultOwned: true,
		AgeHint:      "6 岁以上",
		SessionHint:  "一次 5~10 分钟",
		Learn: entities.JSONStringArray{
			"买种子要先花钱，收成要等 —— 投入在前，回报在后",
			"行情每天会变，同一件东西不同时候卖，价钱不一样",
			"地是一块一块解锁的，想种更多就得先攒够",
			"动物要喂、要摸 —— 有生命的东西得照顾",
		},
		About: entities.JSONStringArray{
			"一块地、一把种子、一个市场。种下去要等它长大，长成了摘下来，拿到市场上卖。卖早了价钱低，卖晚了占着地方。",
			"动物是另一条线：要喂、要摸，产出要自己收。收成和照顾是两件事，孩子会先体会到这一点。",
			"App 里的积分用来买种子，卖出收成换的是兑换券 —— 两种币不互通，孩子没办法自己造钱。",
		},
		Shots: entities.JSONShotList{
			{Src: "/store/cloudfarm/1.webp", Caption: "田里一共 12 块地，种下去等它长大"},
			{Src: "/store/cloudfarm/2.webp", Caption: "动物要喂、要摸，产出得自己收"},
			{Src: "/store/cloudfarm/3.webp", Caption: "市场按当天的行情收你的收成"},
			{Src: "/store/cloudfarm/4.webp", Caption: "鱼塘可以钓鱼，也能养水里的动物"},
		},
		Sort: 100,
	},
	{
		GameID:       "prizepark",
		Name:         "抓奖乐园",
		Emoji:        "🧸",
		Tagline:      "3D 机台上抓奖品，凑成一套再换成兑换券。",
		Kind:         "think",
		Tint:         "berry",
		Status:       entities.GameStatusAvailable,
		PricePoints:  500,
		PriceDiamond: 0,
		DefaultOwned: false,
		AgeHint:      "6 岁以上",
		SessionHint:  "一次 3~8 分钟",
		Learn: entities.JSONStringArray{
			"爪子只能对准一个方向 —— 正面看是正对着，侧面看才知道偏了多少",
			"夹起来不等于拿到：中途掉了就是白玩一次",
			"同样的奖品凑成一套才能一起兑换，缺一件就得接着抓",
			"重复的可以卖回去换币，也可以留着凑套",
		},
		About: entities.JSONStringArray{
			"一台 3D 机台，能切正面和侧面两个视角。爪子对准了再下，夹起来不等于拿到。",
			"奖池里毛绒、手办、零食都有。同样的奖品凑成一套，才能一起兑换成兑换券。",
			"重复的奖品可以卖回去换币，也可以留着凑套 —— 孩子会自己算这笔账。",
		},
		Shots: entities.JSONShotList{
			{Src: "/store/prizepark/1.webp", Caption: "机台能切正面 / 侧面，爪子对准了再下"},
			{Src: "/store/prizepark/2.webp", Caption: "奖池里毛绒、手办、零食都有"},
			{Src: "/store/prizepark/3.webp", Caption: "同样的奖品凑成一套，能一起兑换"},
		},
		Sort: 90,
	},
}

// EnsureGames 幂等地写入游戏种子数据（按 game_id upsert）。
//
// 调用时机：启动时（仅开发/测试环境）或手动执行 cmd/seed。
func EnsureGames(ctx context.Context, repo *repository.GameRepository) error {
	for _, g := range Games {
		if err := repo.Upsert(ctx, g); err != nil {
			logger.ZError("seed game failed", zap.String("game_id", g.GameID), zap.Error(err))
			return err
		}
	}
	logger.ZInfo("games seeded", zap.Int("count", len(Games)))
	return nil
}
