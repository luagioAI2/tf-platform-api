package upload

import (
	"mime/multipart"
	"tf-api/internal/app/config"
)

// GetUploaderFromConfig 从配置创建上传器
func GetUploaderFromConfig() (Uploader, error) {
	cfg := config.Get()

	uploadConfig := UploadConfig{
		Type:              cfg.UploadSettings.Type,
		MaxFileSize:       cfg.UploadSettings.MaxFileSize,
		AllowedExtensions: cfg.UploadSettings.AllowedExtensions,
		Local: LocalConfig{
			StorePath: cfg.UploadSettings.Local.StorePath,
			BaseURL:   cfg.UploadSettings.Local.BaseURL,
		},
		Minio: MinioConfig{
			Endpoint:   cfg.UploadSettings.Minio.Endpoint,
			AccessKey:  cfg.UploadSettings.Minio.AccessKey,
			SecretKey:  cfg.UploadSettings.Minio.SecretKey,
			BucketName: cfg.UploadSettings.Minio.BucketName,
			UseSSL:     cfg.UploadSettings.Minio.UseSSL,
			BaseURL:    cfg.UploadSettings.Minio.BaseURL,
			BasePath:   cfg.UploadSettings.Minio.BasePath,
		},
		AwsS3: AwsS3Config{
			Region:           cfg.UploadSettings.AwsS3.Region,
			Endpoint:         cfg.UploadSettings.AwsS3.Endpoint,
			Bucket:           cfg.UploadSettings.AwsS3.Bucket,
			AccessKey:        cfg.UploadSettings.AwsS3.AccessKey,
			SecretKey:        cfg.UploadSettings.AwsS3.SecretKey,
			BaseURL:          cfg.UploadSettings.AwsS3.BaseURL,
			PathPrefix:       cfg.UploadSettings.AwsS3.PathPrefix,
			S3ForcePathStyle: cfg.UploadSettings.AwsS3.S3ForcePathStyle,
			DisableSSL:       cfg.UploadSettings.AwsS3.DisableSSL,
		},
		CloudflareR2: CloudflareR2Config{
			AccountID:       cfg.UploadSettings.CloudflareR2.AccountID,
			AccessKeyID:     cfg.UploadSettings.CloudflareR2.AccessKeyID,
			SecretAccessKey: cfg.UploadSettings.CloudflareR2.SecretAccessKey,
			Bucket:          cfg.UploadSettings.CloudflareR2.Bucket,
			BaseURL:         cfg.UploadSettings.CloudflareR2.BaseURL,
			PathPrefix:      cfg.UploadSettings.CloudflareR2.PathPrefix,
		},
	}

	return NewUploader(uploadConfig)
}

// QuickUpload 快速上传文件（使用默认配置）
func QuickUpload(file *multipart.FileHeader) (string, string, error) {
	uploader, err := GetUploaderFromConfig()
	if err != nil {
		return "", "", err
	}

	return uploader.UploadFile(file)
}

// QuickDelete 快速删除文件（使用默认配置）
func QuickDelete(key string) error {
	uploader, err := GetUploaderFromConfig()
	if err != nil {
		return err
	}

	return uploader.DeleteFile(key)
}
