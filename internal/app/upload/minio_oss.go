package upload

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinioUploader struct {
	client *minio.Client
	config UploadConfig
}

// NewMinioUploader 创建Minio上传器
func NewMinioUploader(config UploadConfig) (*MinioUploader, error) {
	// 初始化minio客户端
	client, err := minio.New(config.Minio.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.Minio.AccessKey, config.Minio.SecretKey, ""),
		Secure: config.Minio.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化Minio客户端失败: %v", err)
	}

	uploader := &MinioUploader{
		client: client,
		config: config,
	}

	// 确保bucket存在
	if err := uploader.ensureBucket(); err != nil {
		return nil, fmt.Errorf("确保bucket存在失败: %v", err)
	}

	return uploader, nil
}

// ensureBucket 确保bucket存在
func (m *MinioUploader) ensureBucket() error {
	ctx := context.Background()
	bucketName := m.config.Minio.BucketName

	exists, err := m.client.BucketExists(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("检查bucket是否存在失败: %v", err)
	}

	if !exists {
		if err := m.client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("创建bucket失败: %v", err)
		}

		// 设置公共读策略
		policy := fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Principal": "*",
					"Action": ["s3:GetObject"],
					"Resource": ["arn:aws:s3:::%s/*"]
				}
			]
		}`, bucketName)

		if err := m.client.SetBucketPolicy(ctx, bucketName, policy); err != nil {
			return fmt.Errorf("设置bucket策略失败: %v", err)
		}
	}

	return nil
}

// ValidateFile 验证文件
func (m *MinioUploader) ValidateFile(file *multipart.FileHeader) error {
	// 验证文件大小
	if err := ValidateFileSize(file.Size, m.config.MaxFileSize); err != nil {
		return err
	}

	// 验证文件扩展名
	if err := ValidateFileExtension(file.Filename, m.config.AllowedExtensions); err != nil {
		return err
	}

	return nil
}

// UploadFile 上传文件到Minio
func (m *MinioUploader) UploadFile(file *multipart.FileHeader) (string, string, error) {
	// 打开文件
	src, err := file.Open()
	if err != nil {
		return "", "", fmt.Errorf("打开上传文件失败: %v", err)
	}
	defer src.Close()

	// 读取文件内容
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, src); err != nil {
		return "", "", fmt.Errorf("读取文件内容失败: %v", err)
	}

	// 生成唯一文件名
	ext := filepath.Ext(file.Filename)
	name := strings.TrimSuffix(file.Filename, ext)
	hash := md5.Sum([]byte(name + time.Now().String()))
	filename := fmt.Sprintf("%x_%s%s", hash, time.Now().Format("20060102150405"), ext)

	// 构建对象路径
	objectPath := filename
	if m.config.Minio.BasePath != "" {
		objectPath = strings.TrimRight(m.config.Minio.BasePath, "/") + "/" + filename
	}

	// 设置上传超时
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*10)
	defer cancel()

	// 上传文件
	_, err = m.client.PutObject(ctx, m.config.Minio.BucketName, objectPath, &buffer, file.Size, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return "", "", fmt.Errorf("上传文件到Minio失败: %v", err)
	}

	// 构建文件URL
	fileURL := strings.TrimRight(m.config.Minio.BaseURL, "/") + "/" + objectPath

	return fileURL, objectPath, nil
}

// DeleteFile 从Minio删除文件
func (m *MinioUploader) DeleteFile(key string) error {
	if key == "" {
		return fmt.Errorf("文件key不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	err := m.client.RemoveObject(ctx, m.config.Minio.BucketName, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("从Minio删除文件失败: %v", err)
	}

	return nil
}
