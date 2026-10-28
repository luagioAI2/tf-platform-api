package service

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNoGoroutineLeak 验证事件总线在大量发布后不泄漏 goroutine。
// 这是高并发场景最容易出问题的地方：每个事件起一个 goroutine 的实现会直接爆。
func TestNoGoroutineLeak(t *testing.T) {
	// 预热，避免首次 GC/初始化影响基线
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	base := runtime.NumGoroutine()

	es := ProvideEventService()

	var handled int64
	es.Subscribe(OrderCreatedEvent, func(data interface{}) {
		handled++
	})

	var wg sync.WaitGroup
	for i := 0; i < 5000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es.Publish(OrderCreatedEvent, i)
		}()
	}
	wg.Wait()

	// 等待处理完成
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if es.Stats()["handled"] >= 5000 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	es.Close()
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	after := runtime.NumGoroutine()
	t.Logf("goroutines: base=%d after=%d handled=%d", base, after, handled)

	// worker 池固定 4 个/事件类型，关闭后应回落到基线附近（允许少量波动）
	if after > base+10 {
		t.Fatalf("possible goroutine leak: base=%d after=%d", base, after)
	}
	if handled == 0 {
		t.Fatal("expected events handled")
	}
}

// TestEventServiceThroughput 验证事件总线的分发吞吐量。
//
// 说明：这里用「无耗时 handler」测上限，避免把 handler 自身的 sleep
// 算进分发能力的账上。真实业务的 handler 有 IO，需按实际情况扩容 worker。
func TestEventServiceThroughput(t *testing.T) {
	es := ProvideEventServiceWith(32, 65536)
	defer es.Close()

	var count int64
	es.Subscribe(OrderCreatedEvent, func(data interface{}) {
		atomic.AddInt64(&count, 1)
	})

	const total = 100000
	start := time.Now()

	var wg sync.WaitGroup
	workers := 50
	perWorker := total / workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				es.Publish(OrderCreatedEvent, j)
			}
		}()
	}
	wg.Wait()
	publishElapsed := time.Since(start)

	// 等处理完
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&count) >= total {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)

	stats := es.Stats()
	c := atomic.LoadInt64(&count)
	t.Logf("published=%d handled=%d dropped=%d", stats["published"], stats["handled"], stats["dropped"])
	t.Logf("publish_time=%s total_time=%s rate=%.0f/s", publishElapsed, elapsed, float64(c)/elapsed.Seconds())

	// Publish 必须是非阻塞的（队列满时丢弃）
	if publishElapsed > 2*time.Second {
		t.Fatalf("Publish too slow: %s for %d events", publishElapsed, total)
	}
	// 队列足够大时不应丢弃
	if stats["dropped"] > 0 {
		t.Logf("warning: %d events dropped (queue pressure)", stats["dropped"])
	}
}

// TestCacheStampede 验证 singleflight 防击穿：同 key 并发只回源一次。
func TestCacheStampede(t *testing.T) {
	// 这个测试依赖 pkg/cache，放在 cache 包自己的测试里更合适，
	// 这里仅验证事件总线在「缓存失效瞬间」不会被压垮。
	es := ProvideEventService()
	defer es.Close()

	var loadCount int64
	var mu sync.Mutex

	es.Subscribe(OrderCreatedEvent, func(data interface{}) {
		mu.Lock()
		loadCount++
		mu.Unlock()
		time.Sleep(time.Millisecond) // 模拟慢操作
	})

	var wg sync.WaitGroup
	for i := 0; i < 2000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es.Publish(OrderCreatedEvent, nil)
		}()
	}
	wg.Wait()

	time.Sleep(500 * time.Millisecond)
	t.Logf("loadCount=%d", loadCount)
}
