// Package cache 提供两级缓存：L1 进程内 LRU + L2 Redis，并用 singleflight 防击穿。
//
// 为什么需要它（针对 5W 并发）：
//   - 纯 Redis 缓存：热点 key 会被反复读取，Redis QPS 成为瓶颈
//   - 纯本地缓存：多实例间不一致，且内存不可控
//   - 两级缓存 + singleflight：热点数据本地命中（微秒级），未命中时同 key 只回源一次
//
// 使用方式：
//
//	c := cache.New(cache.Config{Name: "user", LocalTTL: 30 * time.Second, RedisTTL: 5 * time.Minute})
//	val, err := c.GetOrLoad(ctx, "user:1001", &user, func(ctx context.Context) (interface{}, error) {
//	    return repo.GetByID(ctx, 1001)
//	})
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrCacheMiss 缓存未命中（内部使用）
var ErrCacheMiss = errors.New("cache miss")

// Config 缓存配置
type Config struct {
	// Name 缓存名（用于指标与 key 前缀）
	Name string
	// LocalTTL 本地缓存过期时间。0 表示不使用本地缓存
	LocalTTL time.Duration
	// RedisTTL Redis 过期时间。0 表示不使用 Redis 缓存
	RedisTTL time.Duration
	// LocalMaxEntries 本地缓存最大条目数，默认 10000
	LocalMaxEntries int
	// Redis 客户端（nil 表示不用 Redis）
	Redis RedisClient
}

// RedisClient 抽象 Redis 操作，便于测试与解耦
type RedisClient interface {
	GetBytes(ctx context.Context, key string) ([]byte, error)
	SetBytes(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) error
}

// Stats 缓存统计
type Stats struct {
	LocalHit   int64 `json:"local_hit"`
	RedisHit   int64 `json:"redis_hit"`
	LoadOK     int64 `json:"load_ok"`
	LoadErr    int64 `json:"load_err"`
	LoadShared int64 `json:"load_shared"` // 被 singleflight 合并的请求数
}

// Cache 两级缓存
type Cache struct {
	cfg Config
	l1  *lru
	sf  singleflight.Group
	rdb RedisClient

	localHit atomic.Int64
	redisHit atomic.Int64
	loadOK   atomic.Int64
	loadErr  atomic.Int64
	shared   atomic.Int64
}

// New 创建缓存实例
func New(cfg Config) *Cache {
	if cfg.LocalMaxEntries <= 0 {
		cfg.LocalMaxEntries = 10000
	}
	c := &Cache{cfg: cfg, rdb: cfg.Redis}
	if cfg.LocalTTL > 0 {
		c.l1 = newLRU(cfg.LocalMaxEntries)
	}
	return c
}

// Name 返回缓存名
func (c *Cache) Name() string { return c.cfg.Name }

// Get 只读缓存（不回源）
func (c *Cache) Get(ctx context.Context, key string) (interface{}, bool) {
	if c.l1 != nil {
		if v, ok := c.l1.get(key); ok {
			c.localHit.Add(1)
			return v, true
		}
	}
	if c.rdb != nil && c.cfg.RedisTTL > 0 {
		b, err := c.rdb.GetBytes(ctx, c.redisKey(key))
		if err == nil && len(b) > 0 {
			var v interface{}
			if json.Unmarshal(b, &v) == nil {
				c.redisHit.Add(1)
				if c.l1 != nil {
					c.l1.set(key, v, c.cfg.LocalTTL)
				}
				return v, true
			}
		}
	}
	return nil, false
}

// Set 写入两级缓存
func (c *Cache) Set(ctx context.Context, key string, value interface{}) error {
	if c.l1 != nil {
		c.l1.set(key, value, c.cfg.LocalTTL)
	}
	if c.rdb != nil && c.cfg.RedisTTL > 0 {
		b, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal cache value: %w", err)
		}
		return c.rdb.SetBytes(ctx, c.redisKey(key), b, c.cfg.RedisTTL)
	}
	return nil
}

// Delete 删除缓存（两级同时删）
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if c.l1 != nil {
		for _, k := range keys {
			c.l1.del(k)
		}
	}
	if c.rdb != nil && len(keys) > 0 {
		rk := make([]string, len(keys))
		for i, k := range keys {
			rk[i] = c.redisKey(k)
		}
		return c.rdb.Del(ctx, rk...)
	}
	return nil
}

// GetOrLoad 缓存优先，未命中时用 singleflight 回源（同 key 并发只回源一次）。
//
// loader 返回值会被序列化进 Redis，因此必须是可 JSON 序列化的类型。
// 注意：返回值经过 JSON 往返后是 map[string]interface{} 或基础类型，
// 如需强类型请自行 json.Unmarshal。
func (c *Cache) GetOrLoad(ctx context.Context, key string, loader func(context.Context) (interface{}, error)) (interface{}, error) {
	if v, ok := c.Get(ctx, key); ok {
		return v, nil
	}

	// singleflight：同一 key 的并发请求只执行一次 loader
	res, err, shared := c.sf.Do(c.redisKey(key), func() (interface{}, error) {
		// 二次检查（可能在等待期间已被其他请求写入）
		if v, ok := c.Get(ctx, key); ok {
			return v, nil
		}

		// 回源用独立 context，避免首个请求取消导致其他等待者全部失败
		loadCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		v, err := loader(loadCtx)
		if err != nil {
			c.loadErr.Add(1)
			return nil, err
		}
		c.loadOK.Add(1)
		_ = c.Set(ctx, key, v)
		return v, nil
	})
	if shared {
		c.shared.Add(1)
	}
	return res, err
}

// GetOrLoadInto 泛型友好版本：把缓存值反序列化到 out。
//
//	out 必须是指针，例如 &user
func (c *Cache) GetOrLoadInto(ctx context.Context, key string, out interface{}, loader func(context.Context) (interface{}, error)) error {
	v, err := c.GetOrLoad(ctx, key, loader)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal cached value: %w", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("unmarshal cached value: %w", err)
	}
	return nil
}

// Stats 返回统计
func (c *Cache) Stats() Stats {
	return Stats{
		LocalHit:   c.localHit.Load(),
		RedisHit:   c.redisHit.Load(),
		LoadOK:     c.loadOK.Load(),
		LoadErr:    c.loadErr.Load(),
		LoadShared: c.shared.Load(),
	}
}

// Len 本地缓存条目数
func (c *Cache) Len() int {
	if c.l1 == nil {
		return 0
	}
	return c.l1.len()
}

// FlushLocal 清空本地缓存（Redis 不动），用于配置热更新
func (c *Cache) FlushLocal() {
	if c.l1 != nil {
		c.l1.clear()
	}
}

func (c *Cache) redisKey(key string) string {
	if c.cfg.Name == "" {
		return key
	}
	return "cache:" + c.cfg.Name + ":" + key
}

// ============================ 简易 LRU ============================

type lruEntry struct {
	key    string
	value  interface{}
	expire time.Time
	prev   *lruEntry
	next   *lruEntry
}

// lru 双向链表 + map 实现的 LRU（带 TTL）
type lru struct {
	mu    sync.Mutex
	cap   int
	size  int
	items map[string]*lruEntry
	head  *lruEntry // 最近使用
	tail  *lruEntry // 最久未用
}

func newLRU(capacity int) *lru {
	return &lru{cap: capacity, items: make(map[string]*lruEntry, capacity)}
}

func (l *lru) get(key string) (interface{}, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.items[key]
	if !ok {
		return nil, false
	}
	if !e.expire.IsZero() && time.Now().After(e.expire) {
		l.removeLocked(e)
		return nil, false
	}
	l.moveToFrontLocked(e)
	return e.value, true
}

func (l *lru) set(key string, value interface{}, ttl time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var expire time.Time
	if ttl > 0 {
		expire = time.Now().Add(ttl)
	}

	if e, ok := l.items[key]; ok {
		e.value = value
		e.expire = expire
		l.moveToFrontLocked(e)
		return
	}

	e := &lruEntry{key: key, value: value, expire: expire}
	l.items[key] = e
	l.pushFrontLocked(e)
	l.size++

	// 超出容量：淘汰最久未用
	for l.size > l.cap && l.tail != nil {
		l.removeLocked(l.tail)
	}
}

func (l *lru) del(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.items[key]; ok {
		l.removeLocked(e)
	}
}

func (l *lru) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = make(map[string]*lruEntry, l.cap)
	l.head, l.tail = nil, nil
	l.size = 0
}

func (l *lru) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

func (l *lru) pushFrontLocked(e *lruEntry) {
	e.prev, e.next = nil, l.head
	if l.head != nil {
		l.head.prev = e
	}
	l.head = e
	if l.tail == nil {
		l.tail = e
	}
}

func (l *lru) removeLocked(e *lruEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		l.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
	delete(l.items, e.key)
	l.size--
}

func (l *lru) moveToFrontLocked(e *lruEntry) {
	if l.head == e {
		return
	}
	// 从当前位置摘除
	if e.prev != nil {
		e.prev.next = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
	// 插到头部
	e.prev, e.next = nil, l.head
	if l.head != nil {
		l.head.prev = e
	}
	l.head = e
	if l.tail == nil {
		l.tail = e
	}
}
