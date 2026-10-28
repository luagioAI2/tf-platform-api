package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"tf-api/internal/app/config"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var WechatServiceSet = wire.NewSet(ProvideWechatService)

// WechatService 微信小程序/公众号登录服务。
//
// 说明：
//   - 小程序用 jscode2session，公众号用 oauth2/access_token
//   - 这里实现小程序的 code2session（最常用）
//   - AppID/AppSecret 从配置读取，缺失时返回明确错误（不静默失败）
type WechatService struct {
	cfg    config.WechatSetting
	client *http.Client
}

// ProvideWechatService 构造函数
func ProvideWechatService() *WechatService {
	return &WechatService{
		cfg: config.Get().WechatSetting,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// WechatSession 微信登录会话
type WechatSession struct {
	OpenID     string
	UnionID    string
	SessionKey string
}

// code2SessionResp 微信接口响应
type code2SessionResp struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// Code2Session 用临时 code 换取 openid/session_key。
//
// 微信文档：https://developers.weixin.qq.com/miniprogram/dev/api-backend/open-api/login/auth.code2Session.html
func (s *WechatService) Code2Session(ctx context.Context, code string) (*WechatSession, error) {
	if s.cfg.AppID == "" || s.cfg.AppSecret == "" {
		// 未配置微信：开发环境返回 mock，生产环境报错
		if config.Get().ServiceSettings.IsProduction() {
			return nil, fmt.Errorf("wechat appid/appsecret not configured")
		}
		logger.ZWarn("wechat not configured, using mock session (dev only)",
			zap.String("code", code))
		return &WechatSession{
			OpenID:  "mock_openid_" + code,
			UnionID: "mock_unionid_" + code,
		}, nil
	}

	api := s.cfg.Code2SessionURL
	if api == "" {
		api = "https://api.weixin.qq.com/sns/jscode2session"
	}

	q := url.Values{}
	q.Set("appid", s.cfg.AppID)
	q.Set("secret", s.cfg.AppSecret)
	q.Set("js_code", code)
	q.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build wechat request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call wechat api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read wechat response: %w", err)
	}

	var r code2SessionResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unmarshal wechat response: %w", err)
	}

	if r.ErrCode != 0 {
		return nil, fmt.Errorf("wechat error %d: %s", r.ErrCode, r.ErrMsg)
	}
	if r.OpenID == "" {
		return nil, fmt.Errorf("wechat returned empty openid")
	}

	return &WechatSession{
		OpenID:     r.OpenID,
		UnionID:    r.UnionID,
		SessionKey: r.SessionKey,
	}, nil
}

// IsConfigured 是否已配置微信参数
func (s *WechatService) IsConfigured() bool {
	return s.cfg.AppID != "" && s.cfg.AppSecret != ""
}
