package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Translator 翻译器接口
type Translator interface {
	T(key string, args ...interface{}) string
	Exists(key string) bool
}

// Manager 多语言管理器
type Manager struct {
	translators map[string]Translator
	fallback    string
	mu          sync.RWMutex
}

// translator 具体翻译器实现
type translator struct {
	messages map[string]string
	locale   string
}

var (
	defaultManager *Manager
	once           sync.Once
)

// Init 初始化多语言管理器
func Init(translationsDir string, fallbackLang string) error {
	var err error
	once.Do(func() {
		defaultManager = &Manager{
			translators: make(map[string]Translator),
			fallback:    fallbackLang,
		}
		err = defaultManager.LoadTranslations(translationsDir)
	})
	return err
}

// LoadTranslations 加载翻译文件
func (m *Manager) LoadTranslations(dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read translations directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".yaml") {
			continue
		}

		locale := strings.TrimSuffix(file.Name(), ".yaml")
		filePath := filepath.Join(dir, file.Name())

		translator, err := m.loadTranslationFile(filePath, locale)
		if err != nil {
			return fmt.Errorf("failed to load translation file %s: %w", filePath, err)
		}

		m.mu.Lock()
		m.translators[locale] = translator
		m.mu.Unlock()
	}

	return nil
}

// loadTranslationFile 加载单个翻译文件
func (m *Manager) loadTranslationFile(filePath, locale string) (Translator, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var messages map[string]string
	if err := yaml.Unmarshal(data, &messages); err != nil {
		return nil, err
	}

	return &translator{
		messages: messages,
		locale:   locale,
	}, nil
}

// GetTranslator 获取指定语言的翻译器
func (m *Manager) GetTranslator(locale string) Translator {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 尝试精确匹配
	if t, exists := m.translators[locale]; exists {
		return t
	}

	// 尝试基础语言匹配 (zh-CN -> zh)
	baseLang := strings.SplitN(locale, "-", 2)[0]
	for lang, t := range m.translators {
		if strings.HasPrefix(lang, baseLang) {
			return t
		}
	}

	// 返回默认翻译器
	if t, exists := m.translators[m.fallback]; exists {
		return t
	}

	// 如果连默认翻译器都不存在，返回空翻译器
	return &translator{messages: make(map[string]string), locale: locale}
}

// T 翻译方法
func (t *translator) T(key string, args ...interface{}) string {
	if msg, exists := t.messages[key]; exists {
		if len(args) > 0 {
			return fmt.Sprintf(msg, args...)
		}
		return msg
	}
	return key // 如果找不到翻译，返回原始key
}

// Exists 检查翻译是否存在
func (t *translator) Exists(key string) bool {
	_, exists := t.messages[key]
	return exists
}

// 全局便捷方法

// T 全局翻译方法
func T(locale, key string, args ...interface{}) string {
	if defaultManager == nil {
		return key
	}
	return defaultManager.GetTranslator(locale).T(key, args...)
}

// GetTranslator 获取翻译器
func GetTranslator(locale string) Translator {
	if defaultManager == nil {
		return &translator{messages: make(map[string]string), locale: locale}
	}
	return defaultManager.GetTranslator(locale)
}

// Exists 检查翻译是否存在
func Exists(locale, key string) bool {
	if defaultManager == nil {
		return false
	}
	return defaultManager.GetTranslator(locale).Exists(key)
}
