package utils

import (
	"net/http"

	"tf-api/internal/app/constant"

	"github.com/gin-gonic/gin"
)

// IsHttps 判断请求是否为 HTTPS（兼容反向代理）
func IsHttps(c *gin.Context) bool {
	return c.GetHeader(constant.HEADER_FORWARDED_PROTO) == "https" || c.Request.TLS != nil
}

// GetProtocol 返回请求协议
func GetProtocol(r *http.Request) string {
	if r.Header.Get(constant.HEADER_FORWARDED_PROTO) == "https" || r.TLS != nil {
		return "https"
	}
	return "http"
}

// GetFullURL 返回 scheme://host
func GetFullURL(r *http.Request) string {
	return GetProtocol(r) + "://" + r.Host
}
