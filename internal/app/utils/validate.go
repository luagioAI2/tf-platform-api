package utils

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/locales/en"
	"github.com/go-playground/locales/es"
	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
	es_translations "github.com/go-playground/validator/v10/translations/es"
	zh_translations "github.com/go-playground/validator/v10/translations/zh"
)

var (
	uni            *ut.UniversalTranslator
	once           sync.Once
	supportLocales []string

	customTagTranslations []struct {
		tag   string
		trans map[string]string
	}
)

func init() {
	supportLocales = []string{"en", "es", "zh"}
	uni = ut.New(en.New(), en.New(), es.New(), zh.New())

	customTagTranslations = []struct {
		tag   string
		trans map[string]string
	}{
		{
			tag: "e164",
			trans: map[string]string{
				"zh": "{{.Field}}格式不正确，应为 E.164 国际电话号码格式",
				"en": "{{.Field}} must be a valid E.164 phone number",
				"es": "{{.Field}} debe ser un número de teléfono E.164 válido",
			},
		},
	}
}

// RegisterTranslations 为 gin 内置 validator 注册多语言翻译。
// 必须在启动时调用一次（见 router.RegisterAPI）。
func RegisterTranslations(v *validator.Validate) error {
	if t, found := uni.GetTranslator("en"); found {
		if err := en_translations.RegisterDefaultTranslations(v, t); err != nil {
			return fmt.Errorf("register en translations: %w", err)
		}
	}
	if t, found := uni.GetTranslator("es"); found {
		if err := es_translations.RegisterDefaultTranslations(v, t); err != nil {
			return fmt.Errorf("register es translations: %w", err)
		}
	}
	if t, found := uni.GetTranslator("zh"); found {
		if err := zh_translations.RegisterDefaultTranslations(v, t); err != nil {
			return fmt.Errorf("register zh translations: %w", err)
		}
	}

	// 用 json tag 作为字段名，前端看到的提示与请求体字段一致
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})

	for _, t := range customTagTranslations {
		for _, locale := range supportLocales {
			if msg := t.trans[locale]; msg != "" {
				RegisterTranslation(v, locale, t.tag, msg)
			}
		}
	}
	return nil
}

// RegisterTranslation 注册单个自定义 tag 的翻译
func RegisterTranslation(v *validator.Validate, locale, tag, translation string) {
	trans, found := uni.GetTranslator(locale)
	if !found {
		return
	}
	_ = v.RegisterTranslation(tag, trans, func(t ut.Translator) error {
		return t.Add(tag, translation, true)
	}, func(t ut.Translator, fe validator.FieldError) string {
		msg, err := t.T(fe.Tag(), fe.Field())
		if err != nil {
			return fe.Error()
		}
		return msg
	})
}

// GetValidate 返回一个带翻译的新验证器（项目默认用 gin 内置的）
func GetValidate() *validator.Validate {
	v := validator.New()
	_ = RegisterTranslations(v)
	return v
}

// GetTrans 获取指定语言的翻译器
func GetTrans(locale ...string) ut.Translator {
	trans, _ := uni.FindTranslator(locale...)
	return trans
}
