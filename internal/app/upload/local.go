package upload

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var mu sync.Mutex

type LocalUploader struct {
	config UploadConfig
}

// NewLocalUploader 创建本地上传器
func NewLocalUploader(config UploadConfig) *LocalUploader {
	return &LocalUploader{
		config: config,
	}
}

// ValidateFile 验证文件
func (l *LocalUploader) ValidateFile(file *multipart.FileHeader) error {
	// 验证文件大小
	if err := ValidateFileSize(file.Size, l.config.MaxFileSize); err != nil {
		return err
	}

	// 验证文件扩展名
	if err := ValidateFileExtension(file.Filename, l.config.AllowedExtensions); err != nil {
		return err
	}

	return nil
}

// UploadFile 上传文件到本地
func (l *LocalUploader) UploadFile(file *multipart.FileHeader) (string, string, error) {
	// 生成唯一文件名
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)

	// 使用MD5加密文件名
	hash := md5.Sum([]byte(name + time.Now().String()))
	filename := fmt.Sprintf("%x_%s%s", hash, time.Now().Format("20060102150405"), ext)

	// 确保存储目录存在
	storePath := l.config.Local.StorePath
	if err := os.MkdirAll(storePath, os.ModePerm); err != nil {
		return "", "", fmt.Errorf("创建存储目录失败: %v", err)
	}

	// 完整文件路径
	fullPath := filepath.Join(storePath, filename)

	// 打开上传的文件
	src, err := file.Open()
	if err != nil {
		return "", "", fmt.Errorf("打开上传文件失败: %v", err)
	}
	defer src.Close()

	// 创建目标文件
	dst, err := os.Create(fullPath)
	if err != nil {
		return "", "", fmt.Errorf("创建目标文件失败: %v", err)
	}
	defer dst.Close()

	// 复制文件内容
	if _, err := io.Copy(dst, src); err != nil {
		return "", "", fmt.Errorf("复制文件内容失败: %v", err)
	}

	// 构建文件URL
	fileURL := strings.TrimRight(l.config.Local.BaseURL, "/") + "/" + filename

	return fileURL, filename, nil
}

// DeleteFile 删除本地文件
func (l *LocalUploader) DeleteFile(key string) error {
	// 检查 key 是否为空
	if key == "" {
		return errors.New("文件key不能为空")
	}

	// 验证 key 是否包含非法字符或尝试访问存储路径之外的文件
	if strings.Contains(key, "..") || strings.ContainsAny(key, `\/:*?"<>|`) {
		return errors.New("非法的文件key")
	}

	// 构建完整文件路径
	fullPath := filepath.Join(l.config.Local.StorePath, key)

	// 检查文件是否存在
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return errors.New("文件不存在")
	}

	// 使用文件锁防止并发删除
	mu.Lock()
	defer mu.Unlock()

	// 删除文件
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("删除文件失败: %v", err)
	}

	return nil
}
