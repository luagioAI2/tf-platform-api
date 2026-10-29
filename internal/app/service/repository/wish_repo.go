package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var WishRepositorySet = wire.NewSet(wire.Struct(new(WishRepository), "*"))

// WishRepository 心愿商城数据访问
type WishRepository struct {
	DB *gorm.DB
}

// NewWishRepository 直接构造（供脚本使用）
func NewWishRepository(db *gorm.DB) *WishRepository {
	return &WishRepository{DB: db}
}

// ============================ 商品目录 ============================

// UpsertItem 幂等写入商品目录（种子数据同步用）
func (r *WishRepository) UpsertItem(ctx context.Context, item *entities.WishItem) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "item_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"kind", "name", "emoji", "tint", "partner_name", "pool", "game_id",
			"price_cents", "age_range", "note", "notice", "buy_url", "buy_label",
			"sort_order", "updated_at",
		}),
	}).Create(item).Error
}

// GetItem 按业务 ID 查询商品
func (r *WishRepository) GetItem(ctx context.Context, itemID string) (*entities.WishItem, error) {
	var item entities.WishItem
	err := r.DB.WithContext(ctx).Where("item_id = ?", itemID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// ListItems 列出商品（按 kind + sort_order 排序）
//
// 排序规则见 entities.CompareWishItem：**盲盒永远在最前**。
func (r *WishRepository) ListItems(ctx context.Context, kind entities.WishKind, includeArchived bool) ([]*entities.WishItem, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.WishItem{})
	if kind != "" {
		tx = tx.Where("kind = ?", kind)
	}
	if !includeArchived {
		tx = tx.Where("archived = ?", false)
	}

	var list []*entities.WishItem
	err := tx.Order("CASE WHEN kind = 'blindbox' THEN 0 ELSE 1 END, sort_order ASC, name ASC").
		Find(&list).Error
	return list, err
}

// ListItemsByIDs 按 ID 批量查询
func (r *WishRepository) ListItemsByIDs(ctx context.Context, itemIDs []string) ([]*entities.WishItem, error) {
	if len(itemIDs) == 0 {
		return nil, nil
	}
	var list []*entities.WishItem
	err := r.DB.WithContext(ctx).Where("item_id IN ?", itemIDs).Find(&list).Error
	return list, err
}

// UpdateItem 更新商品（运营用）
func (r *WishRepository) UpdateItem(ctx context.Context, itemID string, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&entities.WishItem{}).
		Where("item_id = ?", itemID).Updates(fields).Error
}

// ============================ 家长定价 ============================

// GetOffer 查家长的开放设置
func (r *WishRepository) GetOffer(ctx context.Context, uid uint, itemID string) (*entities.WishOffer, error) {
	var o entities.WishOffer
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND item_id = ?", uid, itemID).
		First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOffers 批量查开放设置（列表页避免 N+1）
func (r *WishRepository) ListOffers(ctx context.Context, uid uint, itemIDs []string) (map[string]*entities.WishOffer, error) {
	out := make(map[string]*entities.WishOffer)
	if len(itemIDs) == 0 {
		return out, nil
	}
	var list []*entities.WishOffer
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND item_id IN ?", uid, itemIDs).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	for _, o := range list {
		out[o.ItemID] = o
	}
	return out, nil
}

// UpsertOffer 写入开放设置（家长定价）
func (r *WishRepository) UpsertOffer(ctx context.Context, o *entities.WishOffer) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "uid"}, {Name: "item_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"voucher_price", "diamond_price", "opened_at", "closed_at", "updated_at",
		}),
	}).Create(o).Error
}

// ============================ 心愿单 ============================

// CreateOrder 创建订单
func (r *WishRepository) CreateOrder(ctx context.Context, tx *gorm.DB, o *entities.WishOrder) error {
	db := r.DB
	if tx != nil {
		db = tx
	}
	return db.WithContext(ctx).Create(o).Error
}

// GetOrder 查询订单
func (r *WishRepository) GetOrder(ctx context.Context, uid uint, orderID string) (*entities.WishOrder, error) {
	var o entities.WishOrder
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND order_id = ?", uid, orderID).
		First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOrderForUpdate 事务内加锁读订单（状态跃迁/退款用）
func (r *WishRepository) GetOrderForUpdate(ctx context.Context, tx *gorm.DB, uid uint, orderID string) (*entities.WishOrder, error) {
	var o entities.WishOrder
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("uid = ? AND order_id = ?", uid, orderID).
		First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetActiveOrderByItem 查某商品当前进行中的订单（非终态）
func (r *WishRepository) GetActiveOrderByItem(ctx context.Context, uid uint, itemID string) (*entities.WishOrder, error) {
	var o entities.WishOrder
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND item_id = ? AND status NOT IN ?",
			uid, itemID, []string{string(entities.WishStatusDone), string(entities.WishStatusCancelled)}).
		Order("id DESC").First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetLastCancelled 查最近一条被取消的订单（「上次爸妈说：…」）
func (r *WishRepository) GetLastCancelled(ctx context.Context, uid uint, itemID string) (*entities.WishOrder, error) {
	var o entities.WishOrder
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND item_id = ? AND status = ? AND cancel_reason <> ''",
			uid, itemID, string(entities.WishStatusCancelled)).
		Order("id DESC").First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListActiveOrders 列出进行中的订单（家长待办）
func (r *WishRepository) ListActiveOrders(ctx context.Context, uid uint) ([]*entities.WishOrder, error) {
	var list []*entities.WishOrder
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND status IN ?", uid, []string{
			string(entities.WishStatusWished),
			string(entities.WishStatusCommitted),
			string(entities.WishStatusConfirmed),
		}).
		Order("id DESC").Find(&list).Error
	return list, err
}

// ListOrders 分页查询订单
func (r *WishRepository) ListOrders(ctx context.Context, uid uint, status entities.WishStatus, page, pageSize int) ([]*entities.WishOrder, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.WishOrder{}).Where("uid = ?", uid)
	if status != "" {
		tx = tx.Where("status = ?", string(status))
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 30
	}

	var list []*entities.WishOrder
	err := tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// UpdateOrderCAS 带状态校验的更新（防并发重复扣/退券）
//
// 返回 RowsAffected；0 表示状态已被别的请求改过。
func (r *WishRepository) UpdateOrderCAS(
	ctx context.Context, tx *gorm.DB, uid uint, orderID string,
	from entities.WishStatus, fields map[string]interface{},
) (int64, error) {
	db := r.DB
	if tx != nil {
		db = tx
	}
	// "status" 由调用方放进 fields；这里统一兜一层，
	// 避免误传 WishStatus 类型导致写库时类型不匹配。
	if v, ok := fields["status"]; ok {
		if st, ok := v.(entities.WishStatus); ok {
			fields["status"] = string(st)
		}
	}
	res := db.WithContext(ctx).Model(&entities.WishOrder{}).
		Where("uid = ? AND order_id = ? AND status = ?", uid, orderID, string(from)).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// CountActiveByStatus 统计某状态订单数（家长端红点）
func (r *WishRepository) CountActiveByStatus(ctx context.Context, uid uint, statuses ...entities.WishStatus) (int64, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	list := make([]string, 0, len(statuses))
	for _, s := range statuses {
		list = append(list, string(s))
	}
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.WishOrder{}).
		Where("uid = ? AND status IN ?", uid, list).
		Count(&count).Error
	return count, err
}

// ListRedeemedGameIDs 从心愿单反推「兑换到手过」的游戏 id。
//
// ## 为什么需要它
//
// 游戏类商品的交付 = 装包，而「拥有」一直是从 `installedGames` 派生的。
// 但卸载会把包和存档一起删掉 —— 删完之后这个游戏就
// **既不在已安装列表、也不在钻石账本**（心愿商城不走钻石账本），
// 于是心愿商城认为「孩子没买过」：
//
//   - 盒子卡片变回可兑换 → **再扣一次券**
//   - 盲盒池子把它放回去 → **再开出来一次**
//
// 券是真金白银攒的，卸载一次就白扣一次，而且全程不报错。
// 所以「兑换过」必须有一份**独立的、卸载不掉的记录** —— 就是 wish_order。
func (r *WishRepository) ListRedeemedGameIDs(ctx context.Context, uid uint) ([]string, error) {
	var ids []string
	err := r.DB.WithContext(ctx).Model(&entities.WishOrder{}).
		Where("uid = ? AND opened_game_id <> '' AND status IN ?",
			uid, []string{
				string(entities.WishStatusCommitted),
				string(entities.WishStatusConfirmed),
				string(entities.WishStatusDone),
			}).
		Distinct().Pluck("opened_game_id", &ids).Error
	return ids, err
}

// ListOpenOfferItemIDs 列出**已开放**的商品 id 集合（有价、且没关闭）。
//
// 判据（对齐前端 `wishesAwaitingReply` / `wishShelf`）：
//
//	closed_at == 0 AND voucher_price > 0
//
// `closed_at > 0` 的行**不算开放** —— 家长把它关了，
// 那条心愿重新变回「等你回应」是对的。
func (r *WishRepository) ListOpenOfferItemIDs(ctx context.Context, uid uint) (map[string]bool, error) {
	var ids []string
	err := r.DB.WithContext(ctx).Model(&entities.WishOffer{}).
		Where("uid = ? AND closed_at = 0 AND voucher_price > 0", uid).
		Pluck("item_id", &ids).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ListOpenOffers 列出**已开放**的开放设置（带价，供「上架到兑换列表」用）
//
// 与 `ListOffers` 的区别：这个只返回真正开放的（有价 + 没关闭），
// 而且**不走 `ListOffers` 的 IN 查询** —— 兑换列表要的是「全部已开放的」，
// 而不是「某批商品里哪些开放了」。
func (r *WishRepository) ListOpenOffers(ctx context.Context, uid uint) ([]*entities.WishOffer, error) {
	var list []*entities.WishOffer
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND closed_at = 0 AND voucher_price > 0", uid).
		Find(&list).Error
	return list, err
}

// ListInFlightItemIDs 列出**已在走流程**的商品 id（committed / confirmed）。
//
// 用途：「上架到兑换列表」时要排除这些 —— 那件东西已经付过钱了，
// 再列一次等于鼓励孩子换第二遍。
func (r *WishRepository) ListInFlightItemIDs(ctx context.Context, uid uint) (map[string]bool, error) {
	var ids []string
	err := r.DB.WithContext(ctx).Model(&entities.WishOrder{}).
		Where("uid = ? AND status IN ?", uid, []string{
			string(entities.WishStatusCommitted),
			string(entities.WishStatusConfirmed),
		}).
		Distinct().Pluck("item_id", &ids).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ============================ 兑换码 ============================

// CreateCode 创建兑换码
func (r *WishRepository) CreateCode(ctx context.Context, c *entities.WishCode) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "code_hash"}},
		DoNothing: true,
	}).Create(c).Error
}

// GetCodeByHash 按哈希查码
func (r *WishRepository) GetCodeByHash(ctx context.Context, codeHash string) (*entities.WishCode, error) {
	var c entities.WishCode
	err := r.DB.WithContext(ctx).Where("code_hash = ?", codeHash).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetCodeByHashForUpdate 事务内加锁读码（防并发重复使用）
func (r *WishRepository) GetCodeByHashForUpdate(ctx context.Context, tx *gorm.DB, codeHash string) (*entities.WishCode, error) {
	var c entities.WishCode
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("code_hash = ?", codeHash).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkCodeUsed CAS 标记码已用（防并发重复使用）
func (r *WishRepository) MarkCodeUsed(ctx context.Context, tx *gorm.DB, codeHash string, uid uint, at int64) (int64, error) {
	db := r.DB
	if tx != nil {
		db = tx
	}
	res := db.WithContext(ctx).Model(&entities.WishCode{}).
		Where("code_hash = ? AND used = ?", codeHash, false).
		Updates(map[string]interface{}{
			"used":        true,
			"used_by_uid": uid,
			"used_at":     at,
		})
	return res.RowsAffected, res.Error
}

// ListCodes 列出兑换码（运营用，**不返回明文**）
func (r *WishRepository) ListCodes(ctx context.Context, batch string, used *bool, page, pageSize int) ([]*entities.WishCode, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.WishCode{})
	if batch != "" {
		tx = tx.Where("batch = ?", batch)
	}
	if used != nil {
		tx = tx.Where("used = ?", *used)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}

	var list []*entities.WishCode
	err := tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// CountCodesByBatch 统计批次剩余可用码数
func (r *WishRepository) CountCodesByBatch(ctx context.Context, batch string) (total, unused int64, err error) {
	tx := r.DB.WithContext(ctx).Model(&entities.WishCode{})
	if batch != "" {
		tx = tx.Where("batch = ?", batch)
	}
	if err = tx.Count(&total).Error; err != nil {
		return 0, 0, err
	}
	tx2 := r.DB.WithContext(ctx).Model(&entities.WishCode{}).Where("used = ?", false)
	if batch != "" {
		tx2 = tx2.Where("batch = ?", batch)
	}
	if err = tx2.Count(&unused).Error; err != nil {
		return 0, 0, err
	}
	return total, unused, nil
}
