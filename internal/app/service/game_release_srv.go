package service

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/ed25519sign"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var GameReleaseServiceSet = wire.NewSet(ProvideGameReleaseService)

// HEX64 64 位小写十六进制（SHA-256）
var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// GameReleaseService 游戏版本发布与分发服务。
//
// 职责：
//  1. 发布版本（管理端）
//  2. 构建并**签名** catalog.json（客户端安装流程要用）
//  3. 检查更新 / 发下载地址
type GameReleaseService struct {
	ReleaseRepo *repository.GameReleaseRepository
	GameRepo    *repository.GameRepository
	EventSrv    *EventService

	// 目录缓存（进程内，不引 Redis）
	//
	// 为什么不用 Redis：目录构建只是「遍历 active 版本 + 一次签名」，
	// 很快；缓存的价值只是「别每次请求都重算」。进程内缓存足够，
	// 多实例时各自持一份，发布时各自失效即可 ——
	// 版本数据在数据库里是同一份，实例迟早读到一致的内容。
	catalogMu  sync.RWMutex
	cachedJSON []byte
	cachedSig  string
	cachedAt   time.Time
	cachedChan string
}

// ProvideGameReleaseService 构造函数
func ProvideGameReleaseService(
	releaseRepo *repository.GameReleaseRepository,
	gameRepo *repository.GameRepository,
	eventSrv *EventService,
) *GameReleaseService {
	return &GameReleaseService{
		ReleaseRepo: releaseRepo,
		GameRepo:    gameRepo,
		EventSrv:    eventSrv,
	}
}

// ============================ 发布（管理端） ============================

// Publish 发布一个游戏版本。
//
// 校验：
//   - SHA-256 必须是 64 位小写 hex（不做「补零」「截断」这类宽容匹配）
//   - AssetBase 必须以 `/` 结尾（否则客户端拼资源路径会少一个斜杠）
//   - 体积上限
func (s *GameReleaseService) Publish(ctx context.Context, req *entities.PublishReleaseReq, operatorID uint) (*entities.GameRelease, error) {
	// 游戏必须存在
	g, err := s.GameRepo.GetByGameID(ctx, req.GameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if g == nil {
		return nil, apperrors.WithCode(apperrors.GameNotExist)
	}

	// SHA-256 格式
	sha := strings.ToLower(strings.TrimSpace(req.SHA256))
	if !sha256Re.MatchString(sha) {
		return nil, apperrors.WithCode(apperrors.GameSHA256Invalid)
	}

	// 体积
	maxBytes := config.Get().GameDistribution.MaxPackageBytes
	if maxBytes <= 0 {
		maxBytes = entities.GamePackageMaxBytes
	}
	if req.Size > maxBytes {
		return nil, apperrors.WithCode(apperrors.GamePackageTooLarge)
	}

	// 资源根必须以 / 结尾
	if req.AssetBase != "" && !strings.HasSuffix(req.AssetBase, "/") {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	channel := req.Channel
	if channel == "" {
		channel = entities.ReleaseChannelStable
	}

	// 版本重复检查
	existing, err := s.ReleaseRepo.GetByVersion(ctx, req.GameID, channel, req.Version)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if existing != nil {
		return nil, apperrors.WithCode(apperrors.GameReleaseDuplicate)
	}

	minHostApi := req.MinHostApi
	if minHostApi <= 0 {
		minHostApi = 1
	}

	rel := &entities.GameRelease{
		GameID:       req.GameID,
		Version:      req.Version,
		Channel:      channel,
		URL:          req.URL,
		SHA256:       sha,
		Size:         req.Size,
		AssetBase:    req.AssetBase,
		MinHostApi:   minHostApi,
		Forced:       req.Forced,
		ReleaseNotes: req.ReleaseNotes,
		PublishedAt:  time.Now().Unix(),
		PublishedBy:  operatorID,
	}
	if err := s.ReleaseRepo.Publish(ctx, rel); err != nil {
		return nil, apperrors.WithError(err)
	}

	// 失效目录缓存 —— 否则客户端拿到的是旧目录，装不上新版本
	s.invalidateCatalog()

	logger.ZInfo("game release published",
		zap.String("game_id", rel.GameID),
		zap.String("version", rel.Version),
		zap.String("channel", rel.Channel),
		zap.Uint("operator", operatorID))

	s.EventSrv.Publish(GameReleasePublishedEvent, map[string]interface{}{
		"game_id": rel.GameID,
		"version": rel.Version,
		"channel": rel.Channel,
	})
	return rel, nil
}

// List 列出版本（管理端）
func (s *GameReleaseService) List(ctx context.Context, req *entities.ListReleaseReq, page, pageSize int) ([]*entities.GameReleaseView, int64, error) {
	list, total, err := s.ReleaseRepo.List(ctx, repository.ReleaseQuery{
		GameID:     req.GameID,
		Channel:    req.Channel,
		OnlyActive: req.OnlyActive,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}

	out := make([]*entities.GameReleaseView, 0, len(list))
	for _, r := range list {
		v := &entities.GameReleaseView{GameRelease: *r}
		if g, err := s.GameRepo.GetByGameID(ctx, r.GameID); err == nil && g != nil {
			v.GameName = g.Name
		}
		out = append(out, v)
	}
	return out, total, nil
}

// Delete 删除版本
func (s *GameReleaseService) Delete(ctx context.Context, releaseID uint) error {
	if err := s.ReleaseRepo.Delete(ctx, releaseID); err != nil {
		return apperrors.WithError(err)
	}
	s.invalidateCatalog()
	return nil
}

// SetActive 上架 / 下架一个版本。
//
// ★ **必须失效目录缓存**：目录有 `CatalogCacheSeconds`（默认 300 秒）的缓存，
// 不失效的话「下架」之后客户端最长还能装 5 分钟 —— 运营以为已经停了，
// 用户那边还在往下拉，而且这时候包文件可能已经被清掉了（404）。
// 这条不是优化，是「下架」这个动作本身能不能算数的问题。
func (s *GameReleaseService) SetActive(ctx context.Context, releaseID uint, active bool) error {
	if err := s.ReleaseRepo.SetActive(ctx, releaseID, active); err != nil {
		return apperrors.WithError(err)
	}
	s.invalidateCatalog()
	return nil
}

// ============================ 目录（catalog.json） ============================

// BuildCatalog 构建 catalog.json 的内容（未签名）。
//
// 格式必须与前端 `src/games/verify.ts` 的 `Catalog` 一致：
//
//	{ "version": 1, "games": [ { id, name, version, url, sha256, size?, assetBase? } ] }
func (s *GameReleaseService) BuildCatalog(ctx context.Context, channel string) ([]byte, error) {
	if channel == "" {
		channel = entities.ReleaseChannelStable
	}

	releases, err := s.ReleaseRepo.ListActive(ctx, channel)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	entries := make([]entities.GameCatalogEntry, 0, len(releases))
	for _, r := range releases {
		name := r.GameID
		if g, err := s.GameRepo.GetByGameID(ctx, r.GameID); err == nil && g != nil {
			name = g.Name
		}
		entries = append(entries, entities.GameCatalogEntry{
			ID:        r.GameID,
			Name:      name,
			Version:   r.Version,
			URL:       s.ResolveURL(r.URL),
			SHA256:    r.SHA256,
			Size:      r.Size,
			AssetBase: s.resolveAssetBase(r),
		})
	}

	catalog := entities.GameCatalog{
		Version: entities.CatalogFormatVersion,
		Games:   entries,
	}

	// ⚠️ 不能用 json.MarshalIndent ——签名对象是**字节**，
	// 客户端验签和解析用的是同一串字节。格式化本身没问题（两端一致就行），
	// 但人工编辑过的文件极易出现「验签用的字节 ≠ 解析用的字节」。
	// 统一由服务端产出，客户端只读，所以格式固定即可。
	return json.Marshal(catalog)
}

// resolveAssetBase 计算资源根。
//
// 缺省 = 包所在目录（与前端约定一致）。
func (s *GameReleaseService) resolveAssetBase(r *entities.GameRelease) string {
	if r.AssetBase != "" {
		return r.AssetBase
	}
	url := s.ResolveURL(r.URL)
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return ""
	}
	return url[:i+1]
}

// Catalog 返回签名后的 catalog.json 与 .sig 内容（带缓存）。
//
// 返回 (catalogBytes, signatureBase64, error)。
func (s *GameReleaseService) Catalog(ctx context.Context, channel string) ([]byte, string, error) {
	if channel == "" {
		channel = entities.ReleaseChannelStable
	}

	// 缓存命中
	cfg := config.Get().GameDistribution
	if cfg.CatalogCacheSeconds > 0 {
		s.catalogMu.RLock()
		if s.cachedJSON != nil && s.cachedChan == channel &&
			time.Since(s.cachedAt) < time.Duration(cfg.CatalogCacheSeconds)*time.Second {
			j, sig := s.cachedJSON, s.cachedSig
			s.catalogMu.RUnlock()
			return j, sig, nil
		}
		s.catalogMu.RUnlock()
	}

	// 签名器：没配私钥就报错（fail closed）
	signer, err := ed25519sign.NewSigner(cfg.CatalogPrivateKey)
	if err != nil {
		logger.ZError("catalog signer unavailable", zap.Error(err))
		return nil, "", apperrors.WithCode(apperrors.GameCatalogSignFail)
	}

	body, err := s.BuildCatalog(ctx, channel)
	if err != nil {
		return nil, "", err
	}

	sig := signer.SignToBase64(body)

	// 写回缓存
	if cfg.CatalogCacheSeconds > 0 {
		s.catalogMu.Lock()
		s.cachedJSON, s.cachedSig, s.cachedAt, s.cachedChan = body, sig, time.Now(), channel
		s.catalogMu.Unlock()
	}
	return body, sig, nil
}

// invalidateCatalog 失效目录缓存（发布/删除版本后必须调）
func (s *GameReleaseService) invalidateCatalog() {
	s.catalogMu.Lock()
	s.cachedJSON, s.cachedSig, s.cachedChan = nil, "", ""
	s.catalogMu.Unlock()
}

// ============================ 更新检查 ============================

// CheckUpdate 客户端检查更新。
//
// `req.Installed` 是客户端上报的「game_id → 当前版本」。
// 服务端比对当前生效版本，返回有更新的那些。
func (s *GameReleaseService) CheckUpdate(ctx context.Context, req *entities.GameUpdateCheckReq) (*entities.GameUpdateCheckResp, error) {
	channel := req.Channel
	if channel == "" {
		channel = entities.ReleaseChannelStable
	}

	releases, err := s.ReleaseRepo.ListActive(ctx, channel)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	cfg := config.Get().GameDistribution
	catalogURL := cfg.CatalogBaseURL
	if catalogURL == "" {
		catalogURL = "/api/game/catalog"
	}

	resp := &entities.GameUpdateCheckResp{
		CatalogURL:    catalogURL,
		CatalogSigURL: catalogURL + ".sig",
		Updates:       make([]*entities.GameUpdateItem, 0),
		All:           make([]*entities.GameUpdateItem, 0, len(releases)),
	}

	for _, r := range releases {
		name := r.GameID
		if g, err := s.GameRepo.GetByGameID(ctx, r.GameID); err == nil && g != nil {
			name = g.Name
		}

		item := &entities.GameUpdateItem{
			GameID:       r.GameID,
			Name:         name,
			LatestVer:    r.Version,
			Size:         r.Size,
			SHA256:       r.SHA256,
			URL:          s.ResolveURL(r.URL),
			AssetBase:    s.resolveAssetBase(r),
			MinHostApi:   r.MinHostApi,
			Forced:       r.Forced,
			ReleaseNotes: r.ReleaseNotes,
		}

		if req.Installed != nil {
			item.InstalledVer = req.Installed[r.GameID]
			item.UpdateAvailable = item.InstalledVer != "" && item.InstalledVer != r.Version
		}

		resp.All = append(resp.All, item)
		if item.UpdateAvailable {
			resp.Updates = append(resp.Updates, item)
		}
	}
	return resp, nil
}

// ============================ 下载 ============================

// Download 获取某游戏的下载信息。
//
// 会校验：游戏存在、用户已拥有。
func (s *GameReleaseService) Download(ctx context.Context, uid uint, req *entities.GameDownloadReq) (*entities.GameDownloadResp, error) {
	channel := req.Channel
	if channel == "" {
		channel = entities.ReleaseChannelStable
	}

	g, err := s.GameRepo.GetByGameID(ctx, req.GameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if g == nil {
		return nil, apperrors.WithCode(apperrors.GameNotExist)
	}

	// 已拥有校验（默认拥有的游戏放行）
	if !g.DefaultOwned {
		has, err := s.GameRepo.HasGame(ctx, uid, req.GameID)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		if !has {
			return nil, apperrors.WithCode(apperrors.GameNotOwned)
		}
	}

	var rel *entities.GameRelease
	if req.Version != "" {
		rel, err = s.ReleaseRepo.GetByVersion(ctx, req.GameID, channel, req.Version)
	} else {
		rel, err = s.ReleaseRepo.GetActive(ctx, req.GameID, channel)
	}
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rel == nil {
		return nil, apperrors.WithCode(apperrors.GameReleaseNotExist)
	}

	// 下载计数（失败不影响下载）
	if err := s.ReleaseRepo.IncrDownloadCount(ctx, rel.ID); err != nil {
		logger.ZWarn("incr download count failed", zap.Error(err))
	}

	return &entities.GameDownloadResp{
		GameID:    rel.GameID,
		GameName:  g.Name,
		Version:   rel.Version,
		SHA256:    rel.SHA256,
		Size:      rel.Size,
		URL:       s.ResolveURL(rel.URL),
		AssetBase: s.resolveAssetBase(rel),
		ExpiresIn: config.Get().GameDistribution.SignedURLTTLSeconds,
	}, nil
}

// ResolveURL 把版本记录里的 URL 解析成完整地址。
//
// 规则：
//   - `http://` / `https://` 开头 → 原样返回（已经指向 OSS/CDN）
//   - 其他 → 与 `PackageBaseURL` 拼接
//
// 为什么两种都支持：包本体可能放 GitHub Release（绝对地址），
// 而几十 MB 的贴图放自己的 OSS（相对路径，便于换域名不发版）。
func (s *GameReleaseService) ResolveURL(u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	base := config.Get().GameDistribution.PackageBaseURL
	if base == "" {
		return u
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(u, "/")
}

// PublicKeyBase64 返回当前目录签名公钥（粘到前端 catalogKey.ts 用）。
//
// 用途：换密钥后要能确认「服务端配的」和「前端烧进去的」是同一对，
// 而不是靠「装不上」来发现。
func (s *GameReleaseService) PublicKeyBase64() (string, error) {
	signer, err := ed25519sign.NewSigner(config.Get().GameDistribution.CatalogPrivateKey)
	if err != nil {
		return "", apperrors.WithCode(apperrors.GameCatalogSignFail)
	}
	return signer.PublicKeyBase64(), nil
}

// SelfCheck 自检：签一份空目录再用自己的公钥验签。
//
// 用途：管理端「测试密钥配置」按钮 —— 运维换密钥后点一下就知道对不对，
// 而不是等用户反馈「所有游戏都装不上」。
func (s *GameReleaseService) SelfCheck() (map[string]interface{}, error) {
	signer, err := ed25519sign.NewSigner(config.Get().GameDistribution.CatalogPrivateKey)
	if err != nil {
		return nil, apperrors.WithCode(apperrors.GameCatalogSignFail)
	}

	probe := []byte(`{"version":1,"games":[]}`)
	sig := signer.Sign(probe)

	if err := ed25519sign.VerifyRaw(signer.PublicKey(), probe, sig); err != nil {
		return nil, apperrors.WithError(err)
	}

	return map[string]interface{}{
		"ok":                true,
		"algorithm":         "Ed25519",
		"public_key_base64": signer.PublicKeyBase64(),
		"note":              "把 public_key_base64 粘到前端 src/games/catalogKey.ts 的 CATALOG_PUBLIC_KEY_B64",
	}, nil
}
