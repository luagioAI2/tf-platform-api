package service

import (
	"sync"
	"time"

	"github.com/google/wire"
)

var StateServiceSet = wire.NewSet(ProvideStateService)

// StateChangeListener 状态变化监听器
type StateChangeListener func(key string, oldValue, newValue interface{})

// StateService 进程内状态存储（读写锁保护）。
//
// 注意：仅适用于单实例进程内共享的运行时开关/计数。
// 需要跨实例一致时请用 Redis。
//
// 相比初版：用 RWMutex 替代 channel 串行化。
// 原实现 Set 需要等 worker 调度完成、Get 完全无锁读写 map（data race），
// 在 5W 并发下既慢又不安全。RWMutex 读多写少场景性能更好且语义清晰。
type StateService struct {
	mu        sync.RWMutex
	states    map[string]interface{}
	listeners []StateChangeListener
}

func ProvideStateService() *StateService {
	return &StateService{
		states:    make(map[string]interface{}),
		listeners: make([]StateChangeListener, 0, 4),
	}
}

// Get 读取状态
func (s *StateService) Get(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.states[key]
	return v, ok
}

// Set 写入状态并同步通知监听器（监听器异步执行）
func (s *StateService) Set(key string, value interface{}) {
	s.mu.Lock()
	old := s.states[key]
	s.states[key] = value
	listeners := make([]StateChangeListener, len(s.listeners))
	copy(listeners, s.listeners)
	s.mu.Unlock()

	for _, l := range listeners {
		l := l
		go func() {
			defer func() { _ = recover() }() // 监听器 panic 不影响主流程
			l(key, old, value)
		}()
	}
}

// Delete 删除状态
func (s *StateService) Delete(key string) {
	s.mu.Lock()
	delete(s.states, key)
	s.mu.Unlock()
}

// AddListener 注册状态变化监听器
func (s *StateService) AddListener(l StateChangeListener) {
	if l == nil {
		return
	}
	s.mu.Lock()
	s.listeners = append(s.listeners, l)
	s.mu.Unlock()
}

func (s *StateService) GetBool(key string) bool {
	v, ok := s.Get(key)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func (s *StateService) GetInt(key string) int {
	v, ok := s.Get(key)
	if !ok {
		return 0
	}
	i, _ := v.(int)
	return i
}

func (s *StateService) GetString(key string) string {
	v, ok := s.Get(key)
	if !ok {
		return ""
	}
	str, _ := v.(string)
	return str
}

func (s *StateService) GetDuration(key string) time.Duration {
	v, ok := s.Get(key)
	if !ok {
		return 0
	}
	d, _ := v.(time.Duration)
	return d
}
