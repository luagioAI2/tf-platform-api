package upload

import (
	"errors"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// Uploader 文件上传接口
type Uploader interface {
	UploadFile(file *multipart.FileHeader) (fileURL, fileName string, err error)
	DeleteFile(key string) error
	ValidateFile(file *multipart.FileHeader) error
}

// UploadConfig 上传配置
type UploadConfig struct {
	Type              string
	MaxFileSize       int64
	AllowedExtensions []string
	Local             LocalConfig
	Minio             MinioConfig
	AwsS3             AwsS3Config
	CloudflareR2      CloudflareR2Config
}

type LocalConfig struct {
	StorePath string
	BaseURL   string
}

type MinioConfig struct {
	Endpoint   string
	AccessKey  string
	SecretKey  string
	BucketName string
	UseSSL     bool
	BaseURL    string
	BasePath   string
}

type AwsS3Config struct {
	Region           string
	Endpoint         string
	Bucket           string
	AccessKey        string
	SecretKey        string
	BaseURL          string
	PathPrefix       string
	S3ForcePathStyle bool
	DisableSSL       bool
}

type CloudflareR2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	BaseURL         string
	PathPrefix      string
}

// NewUploader 创建上传器实例
func NewUploader(config UploadConfig) (Uploader, error) {
	switch strings.ToLower(config.Type) {
	case "local":
		return NewLocalUploader(config), nil
	case "minio":
		return NewMinioUploader(config)
	case "aws-s3":
		return NewAwsS3Uploader(config), nil
	case "cloudflare-r2":
		return NewCloudflareR2Uploader(config), nil
	default:
		return NewLocalUploader(config), nil
	}
}

// ValidateFileExtension 验证文件扩展名
func ValidateFileExtension(filename string, allowedExtensions []string) error {
	if len(allowedExtensions) == 0 {
		return nil // 如果没有限制，则允许所有文件
	}

	ext := strings.ToLower(filepath.Ext(filename))
	for _, allowedExt := range allowedExtensions {
		if strings.ToLower(allowedExt) == ext {
			return nil
		}
	}

	return fmt.Errorf("不支持的文件类型: %s, 支持的类型: %v", ext, allowedExtensions)
}

// ValidateFileSize 验证文件大小
func ValidateFileSize(fileSize int64, maxSize int64) error {
	if maxSize > 0 && fileSize > maxSize {
		return fmt.Errorf("文件大小超出限制: %d bytes, 最大允许: %d bytes", fileSize, maxSize)
	}
	return nil
}

// HandleUpload 处理文件上传的通用方法
func HandleUpload(c *gin.Context, uploader Uploader, fieldName string) (string, string, error) {
	file, err := c.FormFile(fieldName)
	if err != nil {
		return "", "", errors.New("获取上传文件失败: " + err.Error())
	}

	// 验证文件
	if err := uploader.ValidateFile(file); err != nil {
		return "", "", err
	}

	// 上传文件
	fileURL, fileName, err := uploader.UploadFile(file)
	if err != nil {
		return "", "", errors.New("文件上传失败: " + err.Error())
	}

	return fileURL, fileName, nil
}
