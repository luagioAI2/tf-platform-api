package config

import (
	"strings"
	"testing"
)

// 启动日志**不能出现任何密钥明文**。
//
// 2026-11-07 实测踩到：白名单式脱敏漏了 `GameDistribution.CatalogPrivateKey`，
// 启动日志里把 Ed25519 私钥整条打了出来。那把钥匙能签一份「任意代码」的
// 游戏目录下发到孩子手机上，而日志通常会被采集/转发 —— 泄漏面比仓库大得多。
//
// 所以现在按**字段名**扫（`secretKeyRe`），这条测试钉两件事：
//  ① 每个像密钥的字段都被换掉；
//  ② 普通字段（端口、路径）**别被误伤** —— 全打星号的话日志就没用了。
func TestPrintWithJSONMasksSecrets(t *testing.T) {
	const (
		dbPass   = "db-password-abcdef"
		jwtKey   = "jwt-sign-key-123456"
		adminKey = "admin-sign-key-abcdef"
		catKey   = "catalog-private-key-material-XXXX"
		appSec   = "wechat-app-secret-YYYY"
		r2Sec    = "cloudflare-r2-secret-ZZZZ"
	)

	config.DBSettings.DataSource = "postgres://user:" + dbPass + "@127.0.0.1:5432/tf"
	config.ServiceSettings.JwtSignKey = jwtKey
	config.ServiceSettings.AdminSignKey = adminKey
	config.GameDistribution.CatalogPrivateKey = catKey
	config.WechatSetting.AppSecret = appSec
	config.UploadSettings.CloudflareR2.SecretAccessKey = r2Sec
	config.ServiceSettings.ListenAddress = "127.0.0.1:8090"

	out := PrintWithJSON()

	for _, secret := range []string{dbPass, jwtKey, adminKey, catKey, appSec, r2Sec} {
		if strings.Contains(out, secret) {
			t.Errorf("启动日志里出现了密钥明文：%q\n%s", secret, out)
		}
	}

	// 普通字段不能被误伤
	if !strings.Contains(out, "8090") {
		t.Errorf("端口被吞掉了（脱敏过头），日志会失去排查价值：%s", out)
	}
	// 掩码保留首尾，运维才能区分「没配」和「配错了」
	if !strings.Contains(out, mask(catKey)) {
		t.Errorf("目录私钥应当被掩码成 %q，实际输出：%s", mask(catKey), out)
	}
}
