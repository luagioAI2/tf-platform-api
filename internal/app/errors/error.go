package errors

import (
	"tf-api/internal/app/i18n"
	"tf-api/pkg/logger"

	"go.uber.org/zap"
)

// ExtraFields 错误附带的上下文信息（会写进日志，并作为 i18n 模板参数）
type ExtraFields map[string]interface{}

// Error 带错误码与错误信息的错误类型。
//
// 与原始实现的关键差异：Error() / TMessage() 不再修改接收者自身字段，
// 因此重复调用不会出现 "where:where:message" 这类重复拼接。
type Error struct {
	Code    int         `json:"code,omitempty"` // 业务错误码
	Message string      `json:"message"`        // 面向用户的错误信息
	Detail  string      `json:"detail"`         // 面向开发的内部细节
	Where   string      `json:"-"`              // 出错位置 Struct.Func
	Fields  ExtraFields `json:"-"`
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = GetErrorMsg(e.Code)
	}
	if e.Where != "" {
		msg = e.Where + ":" + msg
	}
	if e.Detail != "" {
		msg = msg + "," + e.Detail
	}
	return msg
}

// TMessage 返回按 locale 翻译后的错误信息（不修改自身字段）
func (e *Error) TMessage(locale string) string {
	if e.Code == 0 || e.Code == ERROR {
		// 自定义错误：Message 已是最终文案
		if e.Message != "" {
			return e.Message
		}
	}

	key := GetErrorMsg(e.Code)
	translator := i18n.GetTranslator(locale)
	if !translator.Exists(key) {
		if e.Message != "" {
			return e.Message
		}
		return key
	}

	if len(e.Fields) == 0 {
		return translator.T(key)
	}
	// 按字段名排序，保证多语言模板参数顺序稳定
	args := make([]interface{}, 0, len(e.Fields))
	for _, k := range sortedKeys(e.Fields) {
		args = append(args, e.Fields[k])
	}
	return translator.T(key, args...)
}

func sortedKeys(m ExtraFields) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// 简单插入排序，字段数量极少
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func (e *Error) ErrCode() int { return e.Code }

func (e *Error) Track(where, detail string) *Error {
	e.Where = where
	e.Detail = detail
	return e
}

func (e *Error) Log(locale ...string) *Error {
	fields := make([]zap.Field, 0, len(e.Fields)+3)
	for k, v := range e.Fields {
		fields = append(fields, zap.Any(k, v))
	}
	fields = append(fields, zap.Int("code", e.Code))
	if len(locale) > 0 {
		fields = append(fields, zap.String("locale", locale[0]))
	}
	fields = append(fields, zap.String("err", e.Error()))
	logger.ZError("Server Resp Error", fields...)
	return e
}

func (e *Error) WithFields(fields ExtraFields) *Error {
	if e.Fields == nil {
		e.Fields = make(ExtraFields, len(fields))
	}
	for k, v := range fields {
		e.Fields[k] = v
	}
	return e
}

/////////////////////////////////////////////////////////////////////////////

// With 自定义消息（code = ERROR）
func With(message string) *Error {
	return &Error{Code: ERROR, Message: message}
}

func WithFields(message string, fields ExtraFields) *Error {
	return &Error{Code: ERROR, Message: message, Fields: fields}
}

func WithError(err error) *Error {
	e := &Error{Code: ERROR}
	if err == nil {
		return e
	}
	e.Message = err.Error()
	if appErr, ok := err.(*Error); ok {
		e.Code = appErr.Code
		e.Fields = appErr.Fields
		e.Where = appErr.Where
		e.Detail = appErr.Detail
	}
	return e
}

func WithCode(code int) *Error {
	return &Error{Code: code}
}

func WithCodeFields(code int, fields ExtraFields) *Error {
	return &Error{Code: code, Fields: fields}
}

func TruncateError(err error, maxSize int) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > maxSize {
		return msg[:maxSize]
	}
	return msg
}

// Is 判断错误是否为指定错误码
func Is(err error, code int) bool {
	if appErr, ok := err.(*Error); ok {
		return appErr.Code == code
	}
	return false
}
