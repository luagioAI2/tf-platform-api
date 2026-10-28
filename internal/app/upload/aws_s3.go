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

type AwsS3Uploader struct {
	client *s3.Client
	config UploadConfig
}

// NewAwsS3Uploader 创建AWS S3上传器
func NewAwsS3Uploader(uploadConfig UploadConfig) *AwsS3Uploader {
	client := newS3Client(uploadConfig.AwsS3)
	return &AwsS3Uploader{
		client: client,
		config: uploadConfig,
	}
}

// ValidateFile 验证文件
func (a *AwsS3Uploader) ValidateFile(file *multipart.FileHeader) error {
	// 验证文件大小
	if err := ValidateFileSize(file.Size, a.config.MaxFileSize); err != nil {
		return err
	}

	// 验证文件扩展名
	if err := ValidateFileExtension(file.Filename, a.config.AllowedExtensions); err != nil {
		return err
	}

	return nil
}

// UploadFile 上传文件到AWS S3
func (a *AwsS3Uploader) UploadFile(file *multipart.FileHeader) (string, string, error) {
	uploader := manager.NewUploader(a.client)

	// 生成唯一文件名
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	hash := md5.Sum([]byte(name + time.Now().String()))
	filename := fmt.Sprintf("%x_%s%s", hash, time.Now().Format("20060102150405"), ext)

	// 构建对象路径
	objectPath := filename
	if a.config.AwsS3.PathPrefix != "" {
		objectPath = strings.TrimRight(a.config.AwsS3.PathPrefix, "/") + "/" + filename
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		return "", "", fmt.Errorf("打开上传文件失败: %v", err)
	}
	defer src.Close()

	// 上传文件
	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(a.config.AwsS3.Bucket),
		Key:    aws.String(objectPath),
		Body:   src,
	})
	if err != nil {
		return "", "", fmt.Errorf("上传文件到AWS S3失败: %v", err)
	}

	// 构建文件URL
	fileURL := strings.TrimRight(a.config.AwsS3.BaseURL, "/") + "/" + objectPath

	return fileURL, objectPath, nil
}

// DeleteFile 从AWS S3删除文件
func (a *AwsS3Uploader) DeleteFile(key string) error {
	if key == "" {
		return fmt.Errorf("文件key不能为空")
	}

	// 构建完整的对象路径
	objectPath := key
	if a.config.AwsS3.PathPrefix != "" && !strings.HasPrefix(key, a.config.AwsS3.PathPrefix) {
		objectPath = strings.TrimRight(a.config.AwsS3.PathPrefix, "/") + "/" + key
	}

	_, err := a.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(a.config.AwsS3.Bucket),
		Key:    aws.String(objectPath),
	})
	if err != nil {
		return fmt.Errorf("从AWS S3删除文件失败: %v", err)
	}

	// 等待对象删除完成
	waiter := s3.NewObjectNotExistsWaiter(a.client)
	_ = waiter.Wait(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(a.config.AwsS3.Bucket),
		Key:    aws.String(objectPath),
	}, 30*time.Second)

	return nil
}

// newS3Client 创建S3客户端
func newS3Client(s3Config AwsS3Config) *s3.Client {
	// 创建基础配置
	cfg, _ := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(s3Config.Region),
	)

	// 如果提供了访问密钥，使用静态凭据
	if s3Config.AccessKey != "" && s3Config.SecretKey != "" {
		cfg.Credentials = credentials.NewStaticCredentialsProvider(
			s3Config.AccessKey,
			s3Config.SecretKey,
			"",
		)
	}

	// 创建 S3 客户端
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// 如果提供了自定义端点（如MinIO兼容）
		if s3Config.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3Config.Endpoint)
		}
		o.UsePathStyle = s3Config.S3ForcePathStyle
	})

	return client
}
