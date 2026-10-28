package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSingleflightStampede 验证缓存击穿防护：
// 同一个 key 在缓存失效瞬间被 N 个并发请求访问时，loader 只应执行 1 次。
func TestSingleflightStampede(t *testing.T) {
	c := New(Config{
		Name:            "test",
		LocalTTL:        time.Minute,
		RedisTTL:        0, // 只测本地 + singleflight
		LocalMaxEntries: 100,
	})

	var loadCount atomic.Int64
	loader := func(ctx context.Context) (interface{}, error) {
		loadCount.Add(1)
		time.Sleep(50 * time.Millisecond) // 模拟慢查询
		return map[string]interface{}{"id": 1, "name": "test"}, nil
	}

	const concurrency = 500
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetOrLoad(context.Background(), "user:1", loader)
			if err != nil {
				t.Errorf("GetOrLoad error: %v", err)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	loads := loadCount.Load()
	t.Logf("concurrency=%d loader_calls=%d elapsed=%s", concurrency, loads, elapsed)

	// 核心断言：singleflight 应把 500 次并发合并成极少次回源
	if loads > 5 {
		t.Fatalf("singleflight failed: loader called %d times, expected <= 5", loads)
	}
	// 总耗时应该接近单次 loader 耗时，而不是 500 次累加
	if elapsed > time.Second {
		t.Fatalf("too slow: %s (singleflight not effective)", elapsed)
	}
}

// TestCacheHitRate 验证二级缓存命中率
func TestCacheHitRate(t *testing.T) {
	c := New(Config{
		Name:            "hit",
		LocalTTL:        time.Minute,
		RedisTTL:        0,
		LocalMaxEntries: 1000,
	})

	ctx := context.Background()
	var loads atomic.Int64
	loader := func(ctx context.Context) (interface{}, error) {
		loads.Add(1)
		return "value", nil
	}

	// 第一次：回源
	if _, err := c.GetOrLoad(ctx, "k1", loader); err != nil {
		t.Fatal(err)
	}
	// 后续 1000 次：应全部命中本地
	for i := 0; i < 1000; i++ {
		if _, err := c.GetOrLoad(ctx, "k1", loader); err != nil {
			t.Fatal(err)
		}
	}

	if loads.Load() != 1 {
		t.Fatalf("expected 1 load, got %d", loads.Load())
	}

	stats := c.Stats()
	t.Logf("stats: %+v", stats)
	if stats.LocalHit < 1000 {
		t.Fatalf("expected local hits >= 1000, got %d", stats.LocalHit)
	}
}

// TestCacheLRUEviction 验证 LRU 容量限制
func TestCacheLRUEviction(t *testing.T) {
	c := New(Config{
		Name:            "lru",
		LocalTTL:        time.Minute,
		LocalMaxEntries: 10,
	})

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_ = c.Set(ctx, key(i), i)
	}

	if n := c.Len(); n > 10 {
		t.Fatalf("LRU capacity exceeded: %d > 10", n)
	}
	t.Logf("local entries: %d", c.Len())
}

// TestCacheTTL 验证本地缓存过期
func TestCacheTTL(t *testing.T) {
	c := New(Config{
		Name:            "ttl",
		LocalTTL:        50 * time.Millisecond,
		LocalMaxEntries: 10,
	})

	ctx := context.Background()
	_ = c.Set(ctx, "k", "v")

	if _, ok := c.Get(ctx, "k"); !ok {
		t.Fatal("expected cache hit immediately after set")
	}

	time.Sleep(80 * time.Millisecond)

	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatal("expected cache miss after TTL")
	}
}

// TestCacheConcurrent 并发读写不 panic
func TestCacheConcurrent(t *testing.T) {
	c := New(Config{
		Name:            "concurrent",
		LocalTTL:        time.Second,
		LocalMaxEntries: 100,
	})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = c.Set(ctx, key(n%50), n)
		}(i)
		go func(n int) {
			defer wg.Done()
			c.Get(ctx, key(n%50))
		}(i)
	}
	wg.Wait()
}

func key(i int) string {
	return "k" + string(rune('0'+i%10)) + "-" + string(rune('a'+i%26))
}
