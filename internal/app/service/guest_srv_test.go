package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"gorm.io/gorm"
)

// loadGuestTestConfig 给 AuthService 造一份最小配置。
//
// 为什么不直接读 configs/config.yaml：那个文件是 gitignore 的（每台机器
// 单独补，里面有真实密钥），测试依赖它等于「换台机器就跑不过」。
// 这里只写签发 JWT 必需的三个字段。
func loadGuestTestConfig(t *testing.T) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := `ServiceSettings:
  NodeID: 'test'
  ListenAddress: '127.0.0.1:0'
  Environment: 'test'
  Language: 'zh'
  JwtSignKey: 'guest-test-jwt-sign-key-0123456789'
  AdminSignKey: 'guest-test-admin-sign-key-0123456789'
  TokenExpireHours: 2
  TokenRefreshHours: 168
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	config.MustLoad(p)
}

type guestFixture struct {
	auth       *AuthService
	userRepo   *repository.UserRepository
	walletRepo *repository.WalletRepository
	walletSrv  *WalletService
	smsRepo    *repository.SmsCodeRepository
	farmSrv    *FarmService
	db         *gorm.DB
}

func newGuestFixture(t *testing.T) *guestFixture {
	t.Helper()
	loadGuestTestConfig(t)

	db := newTestDB(t)
	userRepo := repository.NewUserRepository(db)
	oauthRepo := &repository.UserOAuthRepository{DB: db}
	tokenRepo := &repository.UserTokenRepository{DB: db}
	smsRepo := &repository.SmsCodeRepository{DB: db}
	walletRepo := repository.NewWalletRepository(db)
	walletSrv := ProvideWalletService(walletRepo)
	auth := ProvideAuthService(userRepo, oauthRepo, tokenRepo, smsRepo, walletSrv, nil, nil)
	farmSrv := ProvideFarmService(repository.NewFarmStateRepository(db), walletRepo)

	return &guestFixture{
		auth:       auth,
		userRepo:   userRepo,
		walletRepo: walletRepo,
		walletSrv:  walletSrv,
		smsRepo:    smsRepo,
		farmSrv:    farmSrv,
		db:         db,
	}
}

// creditCoupon 直接给账号发券（模拟农场卖产出 / 插件发券）。
func (f *guestFixture) creditCoupon(t *testing.T, uid uint, n int64) {
	t.Helper()
	if _, _, err := f.walletSrv.Change(context.Background(), repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyCoupon,
		Delta:    n,
		Source:   entities.SourceFarmMarket,
		Memo:     "测试发券",
	}); err != nil {
		t.Fatalf("发券失败: %v", err)
	}
}

// TestGuestLoginTwoDevicesCoexist 两台设备各自起号，互不影响。
//
// 这条是**回归测试**，不是凑数：`user.mobile` 上有唯一索引，而游客没有手机号。
// GORM 对「没有 default 的 string 零值」会写 `''`，于是第二个游客直接
// `UNIQUE constraint failed: user.mobile` —— 现场表现是「第一个人能进，
// 第二个人一进来就报错」，而且日志里只有一句 500。
// 修法是给 Mobile 加 `default:null`（零值走「省略该列」⇒ 落库 NULL）。
func TestGuestLoginTwoDevicesCoexist(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()

	first, err := f.auth.GuestLogin(ctx, "device-aaaaaaaa-0001", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("第一个游客建号失败: %v", err)
	}
	if !first.IsNewUser {
		t.Error("第一次进入应该 IsNewUser=true")
	}
	if !first.User.IsGuest {
		t.Error("游客账号 IsGuest 应该是 true")
	}

	second, err := f.auth.GuestLogin(ctx, "device-bbbbbbbb-0002", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("第二个游客建号失败（多半是 mobile 唯一索引又炸了）: %v", err)
	}
	if second.User.UID == first.User.UID {
		t.Errorf("两台设备拿到了同一个 uid: %d", first.User.UID)
	}

	// 落库的 mobile 必须是 NULL，而不是空串 —— 空串会让第三个游客继续炸
	var nullCount int64
	if err := f.db.Raw(
		"select count(*) from user where mobile is null").Scan(&nullCount).Error; err != nil {
		t.Fatal(err)
	}
	if nullCount != 2 {
		t.Errorf("游客的 mobile 应该是 NULL，实际 NULL 行数 = %d", nullCount)
	}
}

// TestGuestLoginSameDeviceSameAccount 同一台设备再进来还是同一个人。
func TestGuestLoginSameDeviceSameAccount(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const device = "device-cccccccc-0003"

	first, err := f.auth.GuestLogin(ctx, device, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.auth.GuestLogin(ctx, device, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}

	if second.User.UID != first.User.UID {
		t.Fatalf("同一设备换了个 uid：%d → %d", first.User.UID, second.User.UID)
	}
	if second.IsNewUser {
		t.Error("第二次进来不该再报 IsNewUser")
	}
	if second.AccessToken == "" || second.RefreshToken == "" {
		t.Error("第二次进来也要发令牌")
	}

	// 只应有一个账号
	var n int64
	f.db.Raw("select count(*) from user where is_guest = 1").Scan(&n)
	if n != 1 {
		t.Errorf("同一设备应该只有一个游客账号，实际 %d 个", n)
	}
}

// TestGuestStartsAtZeroPoints 新账号从 0 分开始（注册赠送已取消）。
func TestGuestStartsAtZeroPoints(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()

	resp, err := f.auth.GuestLogin(ctx, "device-dddddddd-0004", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.walletSrv.GetWallet(ctx, resp.User.UID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Points != 0 || w.Coupon != 0 || w.Diamond != 0 {
		t.Errorf("新账号应该是三币全 0，实际 %+v", w)
	}

	// 也不该留下一条 delta=0 的「赠送」流水
	var n int64
	f.db.Raw("select count(*) from ledger_entry where uid = ?", resp.User.UID).Scan(&n)
	if n != 0 {
		t.Errorf("不该有任何流水，实际 %d 条", n)
	}
}

// TestGuestCannotSpendCoupon 游客能花积分、不能花券。
func TestGuestCannotSpendCoupon(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()

	resp, err := f.auth.GuestLogin(ctx, "device-eeeeeeee-0005", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	uid := resp.User.UID

	// 先攒点本钱：券 5 张、积分 100
	f.creditCoupon(t, uid, 5)
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyPoints,
		Delta:    100,
		Source:   entities.SourceTaskReward,
	}); err != nil {
		t.Fatalf("发积分失败: %v", err)
	}

	// 花积分：放行（农场买种子就靠它，拦了等于不能玩）
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyPoints,
		Delta:    -30,
		Source:   entities.SourceFarmPlant,
	}); err != nil {
		t.Fatalf("游客花积分不该被拦: %v", err)
	}

	// 花券：必须被拦
	_, _, err = f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyCoupon,
		Delta:    -1,
		Source:   entities.SourceRedeem,
	})
	if err == nil {
		t.Fatal("游客花券没被拦住")
	}
	if !apperrors.Is(err, apperrors.GuestNeedBind) {
		t.Fatalf("期望 GuestNeedBind，实际 %v", err)
	}

	// 券余额一点没动
	w, err := f.walletSrv.GetWallet(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if w.Coupon != 5 {
		t.Errorf("被拦下之后券余额应该还是 5，实际 %d", w.Coupon)
	}
}

// TestGuestCannotSpendCouponThroughFarm 农场那条通道也得挡。
//
// 农场的账本条目是客户端整包提交的，currency 由客户端挑 ——
// 它直接写 WalletRepo，绕过了 WalletService 的闸门。
func TestGuestCannotSpendCouponThroughFarm(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()

	resp, err := f.auth.GuestLogin(ctx, "device-ffffffff-0006", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	uid := resp.User.UID
	f.creditCoupon(t, uid, 3)
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyPoints,
		Delta:    100,
		Source:   entities.SourceTaskReward,
	}); err != nil {
		t.Fatalf("发积分失败: %v", err)
	}

	_, err = f.farmSrv.Save(ctx, uid, &entities.FarmStateSaveReq{
		Payload: `{"probe":true}`,
		Ledger: []entities.FarmLedgerClaim{{
			Currency: string(entities.CurrencyCoupon),
			Delta:    -1,
			Source:   entities.SourceFarmMarket,
			OpID:     "op-guest-coupon-1",
		}},
	})
	if err == nil {
		t.Fatal("游客通过农场扣券没被拦住")
	}
	if !apperrors.Is(err, apperrors.GuestNeedBind) {
		t.Fatalf("期望 GuestNeedBind，实际 %v", err)
	}

	// 花积分那条照旧放行
	if _, err := f.farmSrv.Save(ctx, uid, &entities.FarmStateSaveReq{
		Payload: `{"probe":true}`,
		Ledger: []entities.FarmLedgerClaim{{
			Currency: string(entities.CurrencyPoints),
			Delta:    -1,
			Source:   entities.SourceFarmPlant,
			OpID:     "op-guest-points-1",
		}},
	}); err != nil {
		t.Fatalf("游客花积分不该被拦: %v", err)
	}
}

// TestGuestBindMobileKeepsUIDAndUnlocks 绑手机号之后：uid 不变、不再是游客、能花券。
func TestGuestBindMobileKeepsUIDAndUnlocks(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const device = "device-gggggggg-0007"

	resp, err := f.auth.GuestLogin(ctx, device, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	uid := resp.User.UID
	f.creditCoupon(t, uid, 4)

	// 造一条可用验证码（BindMobile 走 verifySmsCode）
	const mobile = "+8613800138000"
	if err := f.smsRepo.Create(ctx, &entities.SmsCode{
		Mobile:   mobile,
		CodeHash: hashSmsCode(mobile, "123456"),
		Scene:    entities.SmsSceneBindMobile,
		ExpireAt: time.Now().Unix() + entities.SmsCodeTTLSeconds,
	}); err != nil {
		t.Fatal(err)
	}

	info, err := f.auth.BindMobile(ctx, uid, &entities.BindMobileReq{
		Mobile: mobile,
		Code:   "123456",
	})
	if err != nil {
		t.Fatalf("绑定失败: %v", err)
	}
	if info.IsGuest {
		t.Error("绑完还是 IsGuest=true —— 闸门会一直拦着他花券")
	}
	if info.UID != uid {
		t.Errorf("绑定不该换 uid：%d → %d", uid, info.UID)
	}
	if info.MobileVerified != true {
		t.Error("绑完应该是已验证")
	}

	// 库里也对
	u, err := f.userRepo.GetByID(ctx, uid)
	if err != nil || u == nil {
		t.Fatalf("读回用户失败: %v", err)
	}
	if u.IsGuest {
		t.Error("库里 is_guest 还是 true")
	}
	if u.DeviceID != device {
		t.Errorf("device_id 被清掉了（这台设备下次会另起一个号）：%q", u.DeviceID)
	}
	if u.Mobile != mobile {
		t.Errorf("mobile 没写进去: %q", u.Mobile)
	}

	// 现在能花券了
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyCoupon,
		Delta:    -1,
		Source:   entities.SourceRedeem,
	}); err != nil {
		t.Fatalf("绑定之后花券应该放行: %v", err)
	}

	// 同一台设备再进来自动落到这个账号上（冷启动不丢进度）
	again, err := f.auth.GuestLogin(ctx, device, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if again.User.UID != uid {
		t.Errorf("同一设备再进来换号了：%d → %d", uid, again.User.UID)
	}
	if again.User.IsGuest {
		t.Error("再进来又变回游客了")
	}
}

// TestGuestLoginRejectsBadDeviceID 设备号形状不对就拒，别拿它去建号。
func TestGuestLoginRejectsBadDeviceID(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()

	for _, bad := range []string{"", "   ", "short", "has space in it", "中文设备号中文设备号"} {
		if _, err := f.auth.GuestLogin(ctx, bad, "127.0.0.1", "test"); err == nil {
			t.Errorf("设备号 %q 应该被拒", bad)
		}
	}
}

// ============================ 不带验证码绑定（2026-11-03） ============================
//
// 生产环境没有短信通道 ⇒ 「必须填验证码」等于「永远绑不上」。
// 这三条守的是那条放宽**没有把门开太大**。

// TestGuestBindWithoutCodeNeedsPassword 不带验证码又不设密码 ⇒ 拒。
//
// 这不是「多此一举」：验证码证明号是你的，密码是以后怎么回来。
// 两个都不给，这个账号换台手机就永久丢了，而且丢的时候不会有任何提示。
func TestGuestBindWithoutCodeNeedsPassword(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()

	resp, err := f.auth.GuestLogin(ctx, "device-nocode-0001", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.auth.BindMobile(ctx, resp.User.UID, &entities.BindMobileReq{
		Mobile: "+8613800138001",
	})
	if err == nil {
		t.Fatal("不带验证码也不带密码，居然绑成功了 —— 这个账号以后再也回不来")
	}
	if !apperrors.Is(err, apperrors.BindNeedPassword) {
		t.Errorf("期望 BindNeedPassword，实际 %v", err)
	}
}

// TestGuestBindWithoutCodeWorks 不带验证码 + 设了密码 ⇒ 能绑，而且**真的能登回来**。
//
// ★ 最后那步登录才是这条测试的重点：只断言「绑成功了」不够 ——
//   密码没落库、哈希写坏了、Login 查的不是这个字段，绑完一样是死的。
func TestGuestBindWithoutCodeWorks(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const mobile = "+8613800138002"
	const password = "abc123456"

	resp, err := f.auth.GuestLogin(ctx, "device-nocode-0002", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	uid := resp.User.UID
	f.creditCoupon(t, uid, 3)

	info, err := f.auth.BindMobile(ctx, uid, &entities.BindMobileReq{
		Mobile:   mobile,
		Password: password,
	})
	if err != nil {
		t.Fatalf("不带验证码绑定失败: %v", err)
	}
	if info.IsGuest {
		t.Error("绑完还是 IsGuest=true —— 券还是花不出去")
	}
	if info.UID != uid {
		t.Errorf("绑定不该换 uid：%d → %d", uid, info.UID)
	}
	/* 没验过就是没验过，别假装验过了 —— 后台要靠这个字段挑出来补验 */
	if info.MobileVerified {
		t.Error("没填验证码却记成了「已验证」")
	}

	// 换台设备用手机号 + 密码登回来
	back, err := f.auth.Login(ctx, &entities.LoginReq{Mobile: mobile, Password: password},
		"127.0.0.1", "test", "device-other-0002")
	if err != nil {
		t.Fatalf("绑完登不回来，等于白绑: %v", err)
	}
	if back.User.UID != uid {
		t.Errorf("登回来的不是同一个号：%d → %d", uid, back.User.UID)
	}

	// 券真的能花了（闸门放行）
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      uid,
		Currency: entities.CurrencyCoupon,
		Delta:    -1,
		Source:   entities.SourceRedeem,
		Memo:     "绑完之后花一张",
	}); err != nil {
		t.Errorf("绑完了还是花不了券: %v", err)
	}
}

// TestGuestBindWithoutCodeCannotTakeOthersMobile 别人的手机号，不带验证码抢不走。
//
// 放宽之后唯一的真实风险就是这个：不验证码就能把号绑到自己名下。
// 这里证明它只对「谁都没用过的空号」有效。
func TestGuestBindWithoutCodeCannotTakeOthersMobile(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const mobile = "+8613800138003"

	// A：带验证码绑上（号是它的了）
	a, err := f.auth.GuestLogin(ctx, "device-owner-0003", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.smsRepo.Create(ctx, &entities.SmsCode{
		Mobile:   mobile,
		CodeHash: hashSmsCode(mobile, "654321"),
		Scene:    entities.SmsSceneBindMobile,
		ExpireAt: time.Now().Unix() + entities.SmsCodeTTLSeconds,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.BindMobile(ctx, a.User.UID, &entities.BindMobileReq{
		Mobile: mobile, Code: "654321", Password: "owner123",
	}); err != nil {
		t.Fatalf("A 绑定失败: %v", err)
	}

	// B：想不带验证码把这个号绑到自己名下
	b, err := f.auth.GuestLogin(ctx, "device-thief-0003", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.auth.BindMobile(ctx, b.User.UID, &entities.BindMobileReq{
		Mobile: mobile, Password: "thief123",
	})
	if err == nil {
		t.Fatal("★ 别人的手机号被不带验证码绑走了")
	}
	if !apperrors.Is(err, apperrors.MobileAlreadyReg) {
		t.Errorf("期望 MobileAlreadyReg，实际 %v", err)
	}

	// 而且 B 还是游客（没被写成半成品状态）
	u, err := f.userRepo.GetByID(ctx, b.User.UID)
	if err != nil || u == nil {
		t.Fatalf("读回 B 失败: %v", err)
	}
	if !u.IsGuest {
		t.Error("绑定失败了，B 却被翻成了正式账号")
	}
}

// TestGuestBindWrongCodeStillRejected 填了验证码就必须对 —— 不能因为「可填」就变摆设。
func TestGuestBindWrongCodeStillRejected(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const mobile = "+8613800138004"

	resp, err := f.auth.GuestLogin(ctx, "device-badcode-0004", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.smsRepo.Create(ctx, &entities.SmsCode{
		Mobile:   mobile,
		CodeHash: hashSmsCode(mobile, "111111"),
		Scene:    entities.SmsSceneBindMobile,
		ExpireAt: time.Now().Unix() + entities.SmsCodeTTLSeconds,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = f.auth.BindMobile(ctx, resp.User.UID, &entities.BindMobileReq{
		Mobile: mobile, Code: "999999", Password: "abc123456",
	})
	if err == nil {
		t.Fatal("验证码错了还能绑上")
	}
	if !apperrors.Is(err, apperrors.VerifyCodeInvalid) {
		t.Errorf("期望 VerifyCodeInvalid，实际 %v", err)
	}
}

// TestRegisterWithoutCodeWorks 不带验证码也能注册（同绑定的口径）。
//
// 生产环境没有短信通道 ⇒ 原来「必须填验证码」等于「永远注册不了」。
// ★ 重点仍然是最后那步登录：注册完登不回来，等于注册了个空壳。
func TestRegisterWithoutCodeWorks(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const mobile = "+8613800138005"
	const password = "abc123456"

	resp, err := f.auth.Register(ctx, &entities.RegisterReq{
		Mobile:   mobile,
		Password: password,
	}, "127.0.0.1", "test", "device-reg-0005")
	if err != nil {
		t.Fatalf("不带验证码注册失败: %v", err)
	}
	if resp.User.IsGuest {
		t.Error("注册出来的账号居然是游客")
	}
	if resp.User.MobileVerified {
		t.Error("没填验证码却记成了「已验证」")
	}

	back, err := f.auth.Login(ctx, &entities.LoginReq{Mobile: mobile, Password: password},
		"127.0.0.1", "test", "device-reg-other")
	if err != nil {
		t.Fatalf("注册完登不回来: %v", err)
	}
	if back.User.UID != resp.User.UID {
		t.Errorf("登回来的不是同一个号：%d → %d", resp.User.UID, back.User.UID)
	}

	// 不是游客 ⇒ 花券不该被拦
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      resp.User.UID,
		Currency: entities.CurrencyCoupon,
		Delta:    1,
		Source:   entities.SourceFarmMarket,
		Memo:     "先发一张",
	}); err != nil {
		t.Fatalf("发券失败: %v", err)
	}
	if _, _, err := f.walletSrv.Change(ctx, repository.BalanceChange{
		UID:      resp.User.UID,
		Currency: entities.CurrencyCoupon,
		Delta:    -1,
		Source:   entities.SourceRedeem,
		Memo:     "花掉",
	}); err != nil {
		t.Errorf("正式账号花券被拦了: %v", err)
	}
}

// TestRegisterWithoutCodeRejectsTakenMobile 号被占了就拒 —— 放宽不是「随便占号」。
func TestRegisterWithoutCodeRejectsTakenMobile(t *testing.T) {
	f := newGuestFixture(t)
	ctx := context.Background()
	const mobile = "+8613800138006"

	if _, err := f.auth.Register(ctx, &entities.RegisterReq{
		Mobile: mobile, Password: "first123",
	}, "127.0.0.1", "test", "device-reg-0006a"); err != nil {
		t.Fatalf("第一次注册失败: %v", err)
	}

	_, err := f.auth.Register(ctx, &entities.RegisterReq{
		Mobile: mobile, Password: "second123",
	}, "127.0.0.1", "test", "device-reg-0006b")
	if err == nil {
		t.Fatal("同一个手机号被注册了两次")
	}
	if !apperrors.Is(err, apperrors.MobileAlreadyReg) {
		t.Errorf("期望 MobileAlreadyReg，实际 %v", err)
	}
}
