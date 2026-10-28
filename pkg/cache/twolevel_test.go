package cache

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// fakeRedis 是一个内存版 RedisClient，用于测试 L1+L2 两级缓存逻辑。
// 生产环境用 rds.RedisCache，这里只验证缓存层的正确性。
type fakeRedis struct {
	mu   sync.RWMutex
	data map[string][]byte
	exp  map[string]time.Time

	gets int
	sets int
	dels int
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		data: make(map[string][]byte),
		exp:  make(map[string]time.Time),
	}
}

func (f *fakeRedis) GetBytes(ctx context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++

	if exp, ok := f.exp[key]; ok && time.Now().After(exp) {
		delete(f.data, key)
		delete(f.exp, key)
		return nil, nil
	}
	b, ok := f.data[key]
	if !ok {
		return nil, nil
	}
	// 返回副本，避免调用方修改内部状态
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func (f *fakeRedis) SetBytes(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++

	b := make([]byte, len(val))
	copy(b, val)
	f.data[key] = b
	if ttl > 0 {
		f.exp[key] = time.Now().Add(ttl)
	}
	return nil
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dels++
	for _, k := range keys {
		delete(f.data, k)
		delete(f.exp, k)
	}
	return nil
}

func (f *fakeRedis) stats() (gets, sets, dels int) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.gets, f.sets, f.dels
}

// TestTwoLevelCache L2（Redis）命中后应回填 L1
func TestTwoLevelCache(t *testing.T) {
	rdb := newFakeRedis()
	ctx := context.Background()

	// 第一个缓存实例：回源并写入 L1+L2
	c1 := New(Config{
		Name:            "twolevel",
		LocalTTL:        time.Minute,
		RedisTTL:        5 * time.Minute,
		LocalMaxEntries: 100,
		Redis:           rdb,
	})

	type User struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	var loads int
	loader := func(ctx context.Context) (interface{}, error) {
		loads++
		return &User{ID: 1, Name: "alice"}, nil
	}

	// 第一次：回源
	if _, err := c1.GetOrLoad(ctx, "u:1", loader); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("expected 1 load, got %d", loads)
	}

	// 第二个缓存实例（模拟另一个进程）：L1 空，应命中 L2
	c2 := New(Config{
		Name:            "twolevel",
		LocalTTL:        time.Minute,
		RedisTTL:        5 * time.Minute,
		LocalMaxEntries: 100,
		Redis:           rdb,
	})

	loads = 0
	if _, err := c2.GetOrLoad(ctx, "u:1", loader); err != nil {
		t.Fatal(err)
	}
	if loads != 0 {
		t.Fatalf("expected L2 hit (0 loads), got %d", loads)
	}

	stats := c2.Stats()
	if stats.RedisHit != 1 {
		t.Fatalf("expected redis hit, got %+v", stats)
	}
	t.Logf("c1=%+v c2=%+v", c1.Stats(), c2.Stats())
}

// TestCacheDelete 删除后两级都应失效
func TestCacheDelete(t *testing.T) {
	rdb := newFakeRedis()
	ctx := context.Background()

	c := New(Config{
		Name:            "del",
		LocalTTL:        time.Minute,
		RedisTTL:        5 * time.Minute,
		LocalMaxEntries: 100,
		Redis:           rdb,
	})

	type Item struct {
		V string `json:"v"`
	}

	var loads int
	loader := func(ctx context.Context) (interface{}, error) {
		loads++
		return &Item{V: "x"}, nil
	}

	_, _ = c.GetOrLoad(ctx, "k", loader)
	_, _ = c.GetOrLoad(ctx, "k", loader)
	if loads != 1 {
		t.Fatalf("expected 1 load after cache warm, got %d", loads)
	}

	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}

	_, _ = c.GetOrLoad(ctx, "k", loader)
	if loads != 2 {
		t.Fatalf("expected reload after delete, got %d loads", loads)
	}
}

// TestCacheWithoutRedis 无 Redis 时降级为纯本地缓存
func TestCacheWithoutRedis(t *testing.T) {
	ctx := context.Background()
	c := New(Config{
		Name:            "noredis",
		LocalTTL:        time.Minute,
		RedisTTL:        5 * time.Minute,
		LocalMaxEntries: 100,
		Redis:           nil,
	})

	var loads int
	loader := func(ctx context.Context) (interface{}, error) {
		loads++
		return "v", nil
	}

	for i := 0; i < 10; i++ {
		if _, err := c.GetOrLoad(ctx, "k", loader); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 1 {
		t.Fatalf("expected 1 load with local-only cache, got %d", loads)
	}
}

// TestCacheJSONRoundTrip 验证复杂结构经过缓存后能正确还原
func TestCacheJSONRoundTrip(t *testing.T) {
	rdb := newFakeRedis()
	ctx := context.Background()

	c := New(Config{
		Name:            "roundtrip",
		LocalTTL:        0, // 只走 Redis，强制 JSON 往返
		RedisTTL:        time.Minute,
		LocalMaxEntries: 10,
		Redis:           rdb,
	})

	type Nested struct {
		Tags []string `json:"tags"`
	}
	type Payload struct {
		ID     int               `json:"id"`
		Name   string            `json:"name"`
		Score  float64           `json:"score"`
		Nested Nested            `json:"nested"`
		Meta   map[string]string `json:"meta"`
	}

	orig := &Payload{
		ID:     42,
		Name:   "测试",
		Score:  98.5,
		Nested: Nested{Tags: []string{"a", "b"}},
		Meta:   map[string]string{"k": "v"},
	}

	if err := c.Set(ctx, "p:1", orig); err != nil {
		t.Fatal(err)
	}

	raw, ok := c.Get(ctx, "p:1")
	if !ok {
		t.Fatal("expected cache hit")
	}

	b, _ := json.Marshal(raw)
	var got Payload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	if got.ID != 42 || got.Name != "测试" || got.Score != 98.5 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Nested.Tags) != 2 {
		t.Fatalf("nested tags lost: %+v", got.Nested)
	}
	if got.Meta["k"] != "v" {
		t.Fatalf("meta lost: %+v", got.Meta)
	}
}
