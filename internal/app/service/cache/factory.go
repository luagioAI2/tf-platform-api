package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tf-api/pkg/cache"
	"tf-api/pkg/rds"

	"github.com/google/wire"
)

// CacheSet 缓存层 wire 集合
var CacheSet = wire.NewSet(
	ProvideCacheFactory,
)

// Factory 缓存工厂：按业务域创建/复用缓存实例。
//
// 为什么要工厂而不是每个 Service 自己 new：
//   - 缓存实例需要在进程内复用（同名缓存共享 L1）
//   - 统一在这里配置 TTL，避免各处散落魔法数字
type Factory struct {
	redis     cache.RedisClient
	instances map[string]*cache.Cache
}

// ProvideCacheFactory 构造函数
func ProvideCacheFactory(rdsClient *rds.RedisCache) *Factory {
	var rc cache.RedisClient
	if rdsClient != nil && rdsClient.Available() {
		rc = rdsClient
	}
	return &Factory{
		redis:     rc,
		instances: make(map[string]*cache.Cache, 8),
	}
}

// Get 获取（或创建）指定名字的缓存。默认本地 30s / Redis 5min。
func (f *Factory) Get(name string) *cache.Cache {
	return f.GetWith(name, 30*time.Second, 5*time.Minute)
}

// GetWith 自定义 TTL
func (f *Factory) GetWith(name string, localTTL, redisTTL time.Duration) *cache.Cache {
	if c, ok := f.instances[name]; ok {
		return c
	}
	c := cache.New(cache.Config{
		Name:            name,
		LocalTTL:        localTTL,
		RedisTTL:        redisTTL,
		LocalMaxEntries: 10000,
		Redis:           f.redis,
	})
	f.instances[name] = c
	return c
}

// FlushAllLocal 清空所有缓存的本地层（配置热更新后调用）
func (f *Factory) FlushAllLocal() {
	for _, c := range f.instances {
		c.FlushLocal()
	}
}

// Stats 汇总所有缓存统计
func (f *Factory) Stats() map[string]cache.Stats {
	out := make(map[string]cache.Stats, len(f.instances))
	for name, c := range f.instances {
		out[name] = c.Stats()
	}
	return out
}

// Key 生成缓存键：Key("user", "1001") -> "user:1001"
func Key(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	b := make([]byte, 0, 32)
	for i, p := range parts {
		if i > 0 {
			b = append(b, ':')
		}
		b = append(b, p...)
	}
	return string(b)
}

// 业务缓存名常量（新增业务时在此登记，便于统一管理 TTL）
const (
	CacheUser   = "user"
	CacheConfig = "config"
	CacheOrder  = "order"
	CacheDict   = "dict" // 字典/枚举等低频变更数据
)

// ============================ 泛型辅助 ============================

// Load 泛型缓存读取：命中直接返回 T，未命中回源并写入缓存。
//
// 用法：
//
//	user, err := cache.Load(ctx, factory.Get(cache.CacheUser), "user:1001",
//	    func(ctx context.Context) (*User, error) {
//	        return repo.GetByID(ctx, 1001)
//	    })
//
// 注意：T 必须是可 JSON 序列化的类型；返回值经过缓存往返后
// 仍能正确还原为 T（内部用 json 中转，保证类型一致）。
func Load[T any](ctx context.Context, c *cache.Cache, key string, loader func(context.Context) (T, error)) (T, error) {
	var zero T

	// 1. 先查缓存
	if raw, ok := c.Get(ctx, key); ok {
		if v, ok := raw.(T); ok {
			return v, nil
		}
		// 缓存里是 JSON 反序列化后的通用类型，转回 T
		b, err := json.Marshal(raw)
		if err == nil {
			var out T
			if err := json.Unmarshal(b, &out); err == nil {
				return out, nil
			}
		}
	}

	// 2. 回源（singleflight 合并并发）
	v, err := c.GetOrLoad(ctx, key, func(ctx context.Context) (interface{}, error) {
		return loader(ctx)
	})
	if err != nil {
		return zero, err
	}

	if out, ok := v.(T); ok {
		return out, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return zero, fmt.Errorf("marshal cached value: %w", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return zero, fmt.Errorf("unmarshal cached value: %w", err)
	}
	return out, nil
}
