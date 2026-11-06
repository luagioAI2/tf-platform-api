package entities

import (
	"encoding/json"
	"testing"
)

/* ============================================================
   外链的**序列化**形状（2026-11-06 第七轮）
   ------------------------------------------------------------
   `WishItemView` 嵌了 `WishItem`，两边都有 `BuyURL`，而实体那个是
   `json:"-"`。于是「字段值对了」和「JSON 里真的有 buy_url」是两件事，
   服务层那几条测试只断言了前者。

   顺带记一个实测结论（别照着直觉改）：**同名字段靠深度取胜** ——
   `WishItemView.BuyURL` 在浅层，稳定压住嵌入的那个深层字段，
   把实体的 `json:"-"` 改成 `json:"buy_url"` 也**不会**打架（变异验过）。
   真正会坏的是这三件事，这条测试就是它们的闸：

     · 键名写错（`buy_url` → `buyurl`）：客户端读不到，按钮全没；
     · 有人觉得「实体上已经有一份」就把视图那个字段删了 ⇒ JSON 里没有这个键；
     · `omitempty` 掉了 ⇒ 没配链接时漏出 `""`，契约多出一种「配了空串」。

   所以断言的是**序列化之后的键**，不是结构体字段。
   ============================================================ */

func TestWishItemViewJSONCarriesBuyLink(t *testing.T) {
	view := WishItemView{
		WishItem: WishItem{
			ItemID: "wish-dino",
			Kind:   WishKindToy,
			Name:   "恐龙模型套装",
			// 实体上的同名字段：故意也填上，看它会不会抢了视图的键
			BuyURL:   "https://wrong.example/should-not-win",
			BuyLabel: "不该出现",
		},
		BuyURL:   "https://item.taobao.com/item.htm?id=42",
		BuyLabel: "去淘宝购买",
	}

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["buy_url"] != "https://item.taobao.com/item.htm?id=42" {
		t.Fatalf("JSON 里的 buy_url = %v（期望视图上那条）", got["buy_url"])
	}
	if got["buy_label"] != "去淘宝购买" {
		t.Fatalf("JSON 里的 buy_label = %v", got["buy_label"])
	}
}

// TestWishItemViewJSONOmitsEmptyBuyLink 没配链接时**不许**出现这个键
//
// `omitempty` 是判据的一半：客户端写的是 `buy_url?: string`，
// 键不出现 = undefined = 不渲染按钮。要是这里漏出 `""`，
// 客户端仍然当「没配」处理 —— 但契约上就多了一种「配了空串」的歧义。
func TestWishItemViewJSONOmitsEmptyBuyLink(t *testing.T) {
	view := WishItemView{
		WishItem: WishItem{ItemID: "wish-dino", Kind: WishKindToy, Name: "恐龙模型套装"},
	}

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := got["buy_url"]; ok {
		t.Fatalf("没配链接却带出了 buy_url=%v", got["buy_url"])
	}
	if _, ok := got["buy_label"]; ok {
		t.Fatalf("没配链接却带出了 buy_label=%v", got["buy_label"])
	}
}

// TestWishItemDetailJSONCarriesBuyLink 详情页也走同一份视图（不再有第二套口径）
func TestWishItemDetailJSONCarriesBuyLink(t *testing.T) {
	resp := WishItemDetailResp{
		WishItemView: WishItemView{
			WishItem: WishItem{ItemID: "wish-dino", Kind: WishKindToy, Name: "恐龙模型套装"},
			BuyURL:   "https://item.jd.com/100012043978.html",
			BuyLabel: "去京东看看",
		},
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["buy_url"] != "https://item.jd.com/100012043978.html" {
		t.Fatalf("详情页 JSON 里的 buy_url = %v", got["buy_url"])
	}
	if got["buy_label"] != "去京东看看" {
		t.Fatalf("详情页 JSON 里的 buy_label = %v", got["buy_label"])
	}
}
