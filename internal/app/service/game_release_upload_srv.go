package service

// 游戏包的**上传 → 落盘 → 发布**通道（2026-11-07）。
//
// ## 为什么单独一个文件
//
// `game_release_srv.go` 里 `Publish` 收的是**元数据**（URL / SHA-256 / 体积），
// 前提是「包已经在某个托管上」。可现实是：后台没有上传入口，
// 运营要么手动传 OSS 再手抄哈希，要么根本发不了版。
//
// 所以这里补上「**把包给服务端**」这一半，只管三件事：
//
//	收文件 → 落到静态目录 → 算 SHA-256 → 交给 Publish
//
// 发布之后的校验 / 目录签名 / 缓存失效**全部复用 `Publish`**，不另写一份 ——
// 否则「上传发布的版本」和「手工填 URL 发布的版本」会走散。
//
// ## 为什么落在 StaticPath 下面（而不是 UploadSettings 的 StorePath）
//
// `engine.Static("/public", ./public)` 是现成的静态路由，包放进去就能被
// `PackageBaseURL` 直接指到，**不需要给 API 进程加下载接口**。
// 换 OSS/CDN 时：把这批文件同步上去，`PackageBaseURL` 改成 CDN 地址即可，
// 数据库里的 `url` 是相对路径，不用改。
//
// ⚠️ 这条通道是给「本机/单机部署 + 联调」用的。21 款游戏 173 MB，
// 生产还是要走对象存储（见 `entities/game_release.go` 顶部那段）。

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
)

// uploadPkgExts 允许上传的包扩展名。
//
// 只收**单个插件模块**或**一个 zip**（zip 里是 `<id>.plugin.mjs` + `assets/`）。
// 不收 `.html` —— 那意味着另一种加载模型（iframe），别在这里开口子。
var uploadPkgExts = map[string]bool{
	".mjs": true,
	".js":  true,
	".zip": true,
}

const (
	// gamePackageMaxFiles zip 里最多多少个文件（防「误传整个 node_modules」）
	gamePackageMaxFiles = 4000
	// gamePackageMaxUnzipBytes 解压后总字节上限（防 zip 炸弹）
	gamePackageMaxUnzipBytes = 512 << 20
	// gamePackageEntrySuffix 入口模块的固定后缀
	gamePackageEntrySuffix = ".plugin.mjs"
)

// PublishFromUpload 收一个包文件，落盘后发布。
//
// `req` 里的 URL / SHA256 / Size 会被**覆盖**：它们必须由服务端算，
// 让调用方自己填哈希等于把「包被换掉」的检测交给上传者。
func (s *GameReleaseService) PublishFromUpload(
	ctx context.Context,
	req *entities.PublishReleaseReq,
	fh *multipart.FileHeader,
	operatorID uint,
) (*entities.GameRelease, error) {
	if fh == nil {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !uploadPkgExts[ext] {
		return nil, apperrors.WithCode(apperrors.GamePackageInvalid)
	}

	maxBytes := config.Get().GameDistribution.MaxPackageBytes
	if maxBytes <= 0 {
		maxBytes = entities.GamePackageMaxBytes
	}
	if fh.Size > maxBytes {
		return nil, apperrors.WithCode(apperrors.GamePackageTooLarge)
	}

	/* ★★ 这两个字段会**拼进文件系统路径**，而且下一行就是 `RemoveAll` ——
	   不校验的话 `game_id = "../../.."` 能删到静态目录之外（甚至整盘）。
	   注意 `filepath.Join` 会自己消化 `..`，所以「拼完再看前缀」也不够，
	   必须在拼之前就只允许安全字符。 */
	if !safePathSegment(req.GameID) || !safePathSegment(req.Version) {
		return nil, apperrors.WithCode(apperrors.GamePackageInvalid)
	}

	/* ★★ 重复版本**必须在动盘之前**判掉（2026-11-07 实测踩到）。
	 *
	 * 原来的顺序是「清空目录 → 写文件 → 调 Publish」，而 Publish 自己会做
	 * 版本重复检查并返回 `GameReleaseDuplicate`。于是重发同一个版本时：
	 *   ① 已发布的那份文件被 `RemoveAll` 删掉；
	 *   ② 新文件写进去；
	 *   ③ Publish 报「版本已存在」。
	 * 结果是**数据库里还是旧哈希，目录里已经是新字节** —— 客户端下载后
	 * 哈希对不上，报的是「包被换过」，而运营只看到一句「版本已存在」。
	 *
	 * Publish 里那道检查仍然保留（它才是权威），这里只是把顺序摆正：
	 * 先问一句，问不过就一个字节都不动。 */
	channel := req.Channel
	if channel == "" {
		channel = entities.ReleaseChannelStable
	}
	if existing, err := s.ReleaseRepo.GetByVersion(ctx, req.GameID, channel, req.Version); err != nil {
		return nil, apperrors.WithError(err)
	} else if existing != nil {
		return nil, apperrors.WithCode(apperrors.GameReleaseDuplicate)
	}

	// 目录：<StaticPath>/games/<game_id>/<version>/
	dir := filepath.Join(gamePackageRoot(), req.GameID, req.Version)
	// 先清空：上一次传到一半失败会留下半截文件，客户端下到的就是坏包
	if err := os.RemoveAll(dir); err != nil {
		return nil, apperrors.WithError(fmt.Errorf("清空版本目录失败: %w", err))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, apperrors.WithError(fmt.Errorf("创建版本目录失败: %w", err))
	}

	var entry string
	var err error
	if ext == ".zip" {
		entry, err = extractGameZip(fh, dir)
	} else {
		entry, err = saveGameModule(fh, dir, req.GameID)
	}
	if err != nil {
		return nil, err
	}

	sum, size, err := fileSHA256(filepath.Join(dir, entry))
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	// 相对路径（相对 `/public`）。换域名 / 换 CDN 只改 PackageBaseURL
	req.URL = path.Join("games", req.GameID, req.Version, entry)
	req.SHA256 = sum
	req.Size = size
	if req.AssetBase == "" {
		// 缺省 = 包所在目录（`resolveAssetBase` 会从解析后的 URL 推出来）
		req.AssetBase = ""
	}

	return s.Publish(ctx, req, operatorID)
}

// safePathSegment 判断一个值能不能当**单层**目录名用。
//
// 只放行 `[A-Za-z0-9._-]`，且不许以 `.` 开头（挡住 `.` / `..` / 隐藏目录），
// 不许带分隔符、盘符、空格。游戏 id 和版本号本来就长这样，
// 放宽到「什么都行」只会让路径穿越有缝可钻。
func safePathSegment(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	if strings.HasPrefix(v, ".") {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// gamePackageRoot 包落盘根目录（= 静态路由 `/public` 下的 `games/`）
func gamePackageRoot() string {
	base := config.Get().ServiceSettings.StaticPath
	if base == "" {
		base = "./public"
	}
	return filepath.Join(base, "games")
}

// saveGameModule 把单个模块存成 `<game_id>.plugin.mjs`。
//
// 为什么改名：目录里同时存在多个版本时，`cloudfarm/1.0.3/cloudfarm.plugin.mjs`
// 比 `…/index-9f2a.js` 好排查；而且加载器/资源根都按目录推，不依赖原文件名。
func saveGameModule(fh *multipart.FileHeader, dir, gameID string) (string, error) {
	name := gameID + gamePackageEntrySuffix
	dst := filepath.Join(dir, name)
	if err := copyMultipartTo(fh, dst); err != nil {
		return "", apperrors.WithError(err)
	}
	return name, nil
}

// extractGameZip 解压一个 zip 包，返回入口模块的相对文件名。
//
// 结构约定（和 `public/games/<id>/` 一致）：
//
//	<id>.plugin.mjs
//	assets/…
//
// 也接受「zip 里多套一层目录」（很多人右键压缩就会这样），会自动剥掉
// 那层唯一的顶层目录。
func extractGameZip(fh *multipart.FileHeader, dir string) (string, error) {
	src, err := fh.Open()
	if err != nil {
		return "", apperrors.WithError(err)
	}
	defer src.Close()

	// zip 需要 ReaderAt；先落到临时文件，避免把整包读进内存
	tmp, err := os.CreateTemp("", "gamepkg-*.zip")
	if err != nil {
		return "", apperrors.WithError(err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		return "", apperrors.WithError(err)
	}

	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return "", apperrors.WithCode(apperrors.GamePackageInvalid)
	}
	defer zr.Close()

	if len(zr.File) > gamePackageMaxFiles {
		return "", apperrors.WithCode(apperrors.GamePackageInvalid)
	}

	// 先算一遍公共前缀（有的压缩工具会套一层目录）
	prefix := commonTopDir(zr.File)

	var total int64
	var entry string
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		if !safeRelPath(name) {
			return "", apperrors.WithCode(apperrors.GamePackageInvalid)
		}
		total += int64(f.UncompressedSize64)
		if total > gamePackageMaxUnzipBytes {
			return "", apperrors.WithCode(apperrors.GamePackageTooLarge)
		}

		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", apperrors.WithError(err)
		}
		if err := extractOne(f, dst); err != nil {
			return "", apperrors.WithError(err)
		}
		if strings.HasSuffix(name, gamePackageEntrySuffix) && entry == "" {
			entry = name
		}
	}

	if entry == "" {
		// 没有 `.plugin.mjs` = 传错东西了。**必须报错**，不能「猜一个入口」——
		// 猜错的话客户端会装上一个永远起不来的包，而发布接口返回成功。
		return "", apperrors.WithCode(apperrors.GamePackageInvalid)
	}
	return entry, nil
}

// commonTopDir 所有文件都套在同一个顶层目录里时返回 `dir/`，否则返回空串。
func commonTopDir(files []*zip.File) string {
	top := ""
	for _, f := range files {
		name := strings.TrimPrefix(f.Name, "./")
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		i := strings.Index(name, "/")
		if i < 0 {
			return ""
		}
		cur := name[:i+1]
		if top == "" {
			top = cur
		} else if top != cur {
			return ""
		}
	}
	return top
}

// safeRelPath 挡住 zip slip（`../`、绝对路径、盘符）。
func safeRelPath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return false
	}
	if strings.Contains(name, ":") {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func extractOne(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	// 按 UncompressedSize64 截断，防止头部说谎的包写爆磁盘
	_, err = io.CopyN(out, rc, int64(f.UncompressedSize64))
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

func copyMultipartTo(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

// fileSHA256 算一个文件的 SHA-256（小写 hex）与字节数。
//
// 客户端会拿这个值核对下载到的包，所以**必须算落盘后的那份**，
// 不是算上传流 —— 落盘过程本身也可能出问题（磁盘满会写出截断的文件）。
func fileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
