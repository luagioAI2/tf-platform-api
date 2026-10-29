package entities

// ============================ 「还没走完」的两个口径 ============================
//
// ⚠️ **这两个谓词故意不相等，别拿一个去「统一」另一个。**
//
// 上游 `kid-quest-farm-2d` 在 2026-10-29 的 `6999530` 专门为这件事做了重构，
// 提交信息里写得很直白：
//
//	「上一轮修『红点 1 + 绿标签』时，把兑换记录 tab 的数字改成
//	  committed/confirmed && !openedGameId，但**注释写的是「还在等爸妈
//	  处理的有几单」** —— 代码和注释对不上。而心愿页顶部那张卡用的是
//	  status === 'committed' || 'confirmed'（**含**已交付的数字商品）。
//
//	  所以同一个单子：心愿页说「有 1 单在等爸妈处理」，兑换记录一个徽标
//	  都没有。下一个人看到这个，第一反应一定是「有个数算错了」，然后随便
//	  挑一边改 —— 改哪边都能把已经修好的东西改回去。
//
//	  两个口径其实都对，差别是**读者不同**。」
//
// 所以服务端也把这两个谓词**命名**出来，并在每个调用点注明用的是哪个、
// 为什么不是另一个。内联条件的问题是：看的人只看到表达式，看不到「另一个
// 口径也存在」这个事实。
//
//	| 口径                  | 谁看 | 含已交付的数字商品？ |
//	|-----------------------|------|---------------------|
//	| `NeedsParentAction`   | 家长 | **含** —— 游戏装了，单子还没收尾 |
//	| `ChildPending`        | 孩子 | **不含** —— 标签已经是绿的「已放进游戏柜」，再挂红数字就自相矛盾 |
//
// 对应前端 `domain/wish.ts` 的 `needsParentAction` / `childPending`。

// NeedsParentAction 券已经扣了、**还等着爸妈动手**的单子。
//
// ⚠️ 它**包含已交付的数字商品**：游戏已经装进柜子，但单子还没 `done`，
// 家长仍要收尾（心愿页顶部那张卡就是列这个，文案写「已放进游戏柜，等爸妈收尾」）。
//
// 用于 `WishService.PendingTasks` 的 `action_count`。
func NeedsParentAction(status WishStatus) bool {
	return status == WishStatusCommitted || status == WishStatusConfirmed
}

// ChildPending 孩子那一屏（「📜 兑换记录」）算的「还没兑现」。
//
// 和 `NeedsParentAction` **只差一个条件**：已交付的数字商品不算。
//
// 理由不是数据，是**读者不同** —— 兑换页是孩子的收银台，
// 那一行的标签已经写着绿的「已放进游戏柜」，再挂一个红数字就是自相矛盾。
// 而家长的收尾待办在心愿页顶部那张卡里（`NeedsParentAction`），一张都没漏。
//
// 判据：**同一个 `committed` 单子，心愿页说「有 1 单在等爸妈处理」、
// 兑换记录没有徽标 —— 这不是 bug。**
//
// 用于 `HistoryRow.Pending`。
func ChildPending(status WishStatus, openedGameID string) bool {
	return NeedsParentAction(status) && openedGameID == ""
}
