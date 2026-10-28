package internal

import (
	"os"
	"path/filepath"

	"tf-api/internal/app/config"
	"tf-api/pkg/logger"

	"go.uber.org/zap"
)

// ensureStaticDir 确保静态目录存在（gin.Static 要求目录已存在）
func ensureStaticDir() string {
	dir := config.Get().ServiceSettings.StaticPath
	if dir == "" {
		dir = "./public"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.ZWarn("create static dir failed", zap.String("dir", dir), zap.Error(err))
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}
