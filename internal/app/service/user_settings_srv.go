package service

import (
	"context"
	"encoding/json"
	"strings"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
)

var UserSettingsServiceSet = wire.NewSet(ProvideUserSettingsService)

// UserSettingsService 家长设置（整份 JSON 存取）
type UserSettingsService struct {
	Repo *repository.UserSettingsRepository
}

// ProvideUserSettingsService 构造函数
func ProvideUserSettingsService(repo *repository.UserSettingsRepository) *UserSettingsService {
	return &UserSettingsService{Repo: repo}
}

// Get 读设置。没设过时返回 `nil, nil`（调用方用客户端默认值）。
func (s *UserSettingsService) Get(ctx context.Context, uid uint) (*entities.UserSettings, error) {
	row, err := s.Repo.GetByUID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return row, nil
}

// Save 整份覆盖。
//
// ## 服务端**不解释**这份 JSON
//
// 字段是客户端的事（二十多个，每个版本都在变）。服务端只保证三件事：
//  1. 是合法的 JSON；
//  2. 顶层是个**对象**（不是数组 / 字符串 / 数字）；
//  3. 不超过 `MaxUserSettingsBytes`。
//
// 为什么第 2 条要单独查：`json.Valid("123")` 是 true，
// 存进去之后客户端 `JSON.parse` 拿到一个数字，再 `{...数字}` 就得到 `{}`
// —— 全家的设置被静默清空，而且每一步都不报错。
//
// ## 为什么不校验具体字段
//
// 校验字段就得跟着客户端一起改，而这个接口的全部意义就是
// 「客户端加字段不用动服务端」。真正该管的是大小和形状。
func (s *UserSettingsService) Save(ctx context.Context, uid uint, payload string, version int64) (*entities.UserSettings, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, apperrors.WithCode(apperrors.SettingsInvalid)
	}
	if len(trimmed) > entities.MaxUserSettingsBytes {
		return nil, apperrors.WithCodeFields(apperrors.SettingsInvalid, apperrors.ExtraFields{
			"max": entities.MaxUserSettingsBytes,
			"got": len(trimmed),
		})
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, apperrors.WithCode(apperrors.SettingsInvalid)
	}
	// 顶层必须是对象：见上面第 2 条的理由
	if !strings.HasPrefix(trimmed, "{") {
		return nil, apperrors.WithCode(apperrors.SettingsInvalid)
	}

	row := &entities.UserSettings{
		UID:     uid,
		Payload: trimmed,
		Size:    len(trimmed),
		Version: version,
	}
	if err := s.Repo.Upsert(ctx, row); err != nil {
		return nil, apperrors.WithError(err)
	}

	// Upsert 走的是 `OnConflict`，`Create` 不会回填已有行的 ID / CreatedAt ⇒
	// 读回来一次，让调用方拿到真实的那一行（尤其是 `updated_at`）。
	return s.Get(ctx, uid)
}
