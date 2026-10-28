package ginx

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tf-api/internal/app/constant"
	"tf-api/internal/app/errors"
	"tf-api/internal/app/i18n"
	"tf-api/internal/app/utils"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// Resp 统一响应体。
//
//	成功：{"code":200,"data":{...}}
//	失败：{"code":10020101,"error":"订单不存在"}
//
// 注意：
//   - HTTP 状态码恒为 200（鉴权失败/限流/超时除外），业务状态看 code
//   - Data 不加 omitempty：否则 data 为 0/false/"" 时字段会消失，前端难判断
type Resp struct {
	Code int         `json:"code"`            // 业务错误码，200 = 成功
	Msg  string      `json:"error,omitempty"` // 为空时该字段不出现
	Data interface{} `json:"data"`
}

// ============================ 当前用户 ============================

// Mine 返回上下文中的用户 ID，未登录返回 0。
func Mine(c *gin.Context) uint {
	v, exists := c.Get("userID")
	if !exists || v == nil {
		return 0
	}
	switch val := v.(type) {
	case uint:
		return val
	case string:
		id, _ := strconv.ParseUint(val, 10, 64)
		return uint(id)
	case float64:
		return uint(val)
	default:
		return 0
	}
}

// MustMine 返回当前用户 ID；未登录时写入未登录错误并 abort。
// 调用后必须检查 ok：ok == false 时直接 return。
func MustMine(c *gin.Context) (uint, bool) {
	uid := Mine(c)
	if uid == 0 {
		RespErr(c, errors.WithCode(errors.UnLogin))
		c.Abort()
		return 0, false
	}
	return uid, true
}

// ============================ 参数解析 ============================

// ParseJSON 解析 JSON 请求体
func ParseJSON(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindJSON(obj); err != nil {
		return errors.WithError(err).Track("ginx.ParseJSON", fmt.Sprintf("parse request json failed: %s", err.Error()))
	}
	return nil
}

// ParseQuery 解析 query 参数
func ParseQuery(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindQuery(obj); err != nil {
		return errors.WithError(err).Track("ginx.ParseQuery", fmt.Sprintf("parse request query failed: %s", err.Error()))
	}
	return nil
}

// ParseForm 解析 form 表单
func ParseForm(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindWith(obj, binding.Form); err != nil {
		return errors.WithError(err).Track("ginx.ParseForm", fmt.Sprintf("parse request form failed: %s", err.Error()))
	}
	return nil
}

// ParseParamID 解析路径参数为 uint64
func ParseParamID(c *gin.Context, key string) uint64 {
	id, err := strconv.ParseUint(c.Param(key), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// ============================ 响应 ============================

// RespSucc 成功响应
func RespSucc(c *gin.Context, v interface{}) {
	c.JSON(http.StatusOK, &Resp{Code: errors.SUCCESS, Data: v})
}

// RespSuccMsg 成功响应并附带提示文案
func RespSuccMsg(c *gin.Context, msg string, v interface{}) {
	c.JSON(http.StatusOK, &Resp{Code: errors.SUCCESS, Msg: msg, Data: v})
}

// RespErr 失败响应：自动区分参数校验错误 / 业务错误 / 普通错误，并写日志。
func RespErr(c *gin.Context, err error) {
	locale := i18n.GetPreferredLanguage(c)

	switch e := err.(type) {
	case validator.ValidationErrors:
		ResJSON(c, http.StatusOK, &Resp{
			Code: errors.ValidationParamError,
			Msg:  transFirstValidationError(locale, e),
		})
	case *errors.Error:
		ResJSON(c, http.StatusOK, &Resp{
			Code: e.ErrCode(),
			Msg:  e.TMessage(locale),
		})
	default:
		ResJSON(c, http.StatusOK, &Resp{
			Code: errors.ERROR,
			Msg:  err.Error(),
		})
	}

	MarkError(c, err)
}

// RespErrCode 直接用错误码返回（无需构造 error 对象）
func RespErrCode(c *gin.Context, code int) {
	locale := i18n.GetPreferredLanguage(c)
	ResJSON(c, http.StatusOK, &Resp{
		Code: code,
		Msg:  i18n.T(locale, errors.GetErrorMsg(code)),
	})
}

// ResJSON 以指定 HTTP 状态码输出
func ResJSON(c *gin.Context, status int, v interface{}) {
	c.JSON(status, v)
}

// MarkError 记录错误日志（带 requestID）
func MarkError(c *gin.Context, err error) {
	locale := i18n.GetPreferredLanguage(c)
	reqID := c.GetString("requestID")

	if appErr, ok := err.(*errors.Error); ok {
		appErr.WithFields(errors.ExtraFields{"requestID": reqID}).Log(locale)
		return
	}
	errors.WithError(err).WithFields(errors.ExtraFields{"requestID": reqID}).Log(locale)
}

// ============================ 校验错误翻译 ============================

func transValidationError(locale string, validationErrors validator.ValidationErrors) string {
	ut := utils.GetTrans(locale)
	msgs := make([]string, 0, len(validationErrors))
	for _, valErr := range validationErrors {
		msgs = append(msgs, valErr.Translate(ut))
	}
	return strings.Join(msgs, ",")
}

func transFirstValidationError(locale string, validationErrors validator.ValidationErrors) string {
	ut := utils.GetTrans(locale)
	if len(validationErrors) > 0 {
		return validationErrors[0].Translate(ut)
	}
	return ""
}

// ============================ 客户端信息 ============================

// GetClientIP 获取客户端真实 IP（优先代理头）
func GetClientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if ips := strings.Split(xff, ","); len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return xri
	}
	return c.ClientIP()
}

// SetAuthTokens 把新令牌写入响应头，供前端读取。
// 不写 Cookie：跨域 + SameSite 场景下 Cookie 反而增加 CSRF 面。
func SetAuthTokens(c *gin.Context, accessToken, refreshToken string) {
	c.Header(constant.ACCESS_TOKEN, accessToken)
	c.Header(constant.REFRESH_TOKEN, refreshToken)
}
