package upload

import (
	"context"
	"crypto/md5"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type CloudflareR2Uploader struct {
	client *s3.Client
	config UploadConfig
}

// NewCloudflareR2Uploader 创建Cloudflare R2上传器
func NewCloudflareR2Uploader(uploadConfig UploadConfig) *CloudflareR2Uploader {
	client := newR2Client(uploadConfig.CloudflareR2)
	return &CloudflareR2Uploader{
		client: client,
		config: uploadConfig,
	}
}

// ValidateFile 验证文件
func (c *CloudflareR2Uploader) ValidateFile(file *multipart.FileHeader) error {
	// 验证文件大小
	if err := ValidateFileSize(file.Size, c.config.MaxFileSize); err != nil {
		return err
	}

	// 验证文件扩展名
	if err := ValidateFileExtension(file.Filename, c.config.AllowedExtensions); err != nil {
		return err
	}

	return nil
}

// UploadFile 上传文件到Cloudflare R2
func (c *CloudflareR2Uploader) UploadFile(file *multipart.FileHeader) (string, string, error) {
	uploader := manager.NewUploader(c.client)

	// 生成唯一文件名
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	hash := md5.Sum([]byte(name + time.Now().String()))
	filename := fmt.Sprintf("%x_%s%s", hash, time.Now().Format("20060102150405"), ext)

	// 构建对象路径
	objectPath := filename
	if c.config.CloudflareR2.PathPrefix != "" {
		objectPath = strings.TrimRight(c.config.CloudflareR2.PathPrefix, "/") + "/" + filename
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		return "", "", fmt.Errorf("打开上传文件失败: %v", err)
	}
	defer src.Close()

	// 上传文件
	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(c.config.CloudflareR2.Bucket),
		Key:    aws.String(objectPath),
		Body:   src,
	})
	if err != nil {
		return "", "", fmt.Errorf("上传文件到Cloudflare R2失败: %v", err)
	}

	// 构建文件URL
	fileURL := strings.TrimRight(c.config.CloudflareR2.BaseURL, "/") + "/" + objectPath

	return fileURL, objectPath, nil
}

// DeleteFile 从Cloudflare R2删除文件
func (c *CloudflareR2Uploader) DeleteFile(key string) error {
	if key == "" {
		return fmt.Errorf("文件key不能为空")
	}

	// 构建完整的对象路径
	objectPath := key
	if c.config.CloudflareR2.PathPrefix != "" && !strings.HasPrefix(key, c.config.CloudflareR2.PathPrefix) {
		objectPath = strings.TrimRight(c.config.CloudflareR2.PathPrefix, "/") + "/" + key
	}

	_, err := c.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(c.config.CloudflareR2.Bucket),
		Key:    aws.String(objectPath),
	})
	if err != nil {
		return fmt.Errorf("从Cloudflare R2删除文件失败: %v", err)
	}

	// 等待对象删除完成
	waiter := s3.NewObjectNotExistsWaiter(c.client)
	_ = waiter.Wait(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(c.config.CloudflareR2.Bucket),
		Key:    aws.String(objectPath),
	}, 30*time.Second)

	return nil
}

// newR2Client 创建Cloudflare R2客户端
func newR2Client(r2Config CloudflareR2Config) *s3.Client {
	// Cloudflare R2 端点格式
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", r2Config.AccountID)

	// 创建自定义配置
	cfg, _ := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("auto"), // Cloudflare R2 使用 "auto" 区域
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			r2Config.AccessKeyID,
			r2Config.SecretAccessKey,
			"",
		)),
	)

	// 创建 S3 客户端，指定自定义端点
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = false // R2 不需要强制路径样式
	})

	return client
}
