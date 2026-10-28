package service

import (
	"sync"
	"testing"
	"time"
)

// TestEventServiceConcurrent 验证事件总线在并发订阅/发布下无 data race。
// 运行：go test -race ./internal/app/service/
func TestEventServiceConcurrent(t *testing.T) {
	es := ProvideEventService()
	defer es.Close()

	var handled int64
	var mu sync.Mutex

	// 并发订阅
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es.Subscribe(OrderCreatedEvent, func(data interface{}) {
				mu.Lock()
				handled++
				mu.Unlock()
			})
		}()
	}
	wg.Wait()

	// 并发发布
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es.Publish(OrderCreatedEvent, "payload")
		}()
	}
	wg.Wait()

	// 等待异步处理完成
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if es.Stats()["handled"] > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	stats := es.Stats()
	t.Logf("stats: %+v", stats)

	if stats["published"] == 0 {
		t.Fatal("expected published > 0")
	}
	if stats["handled"] == 0 {
		t.Fatal("expected handled > 0")
	}
}

// TestEventServicePanicRecover 验证 handler panic 不会导致进程崩溃
func TestEventServicePanicRecover(t *testing.T) {
	es := ProvideEventService()
	defer es.Close()

	es.Subscribe(UserRegisteredEvent, func(data interface{}) {
		panic("boom")
	})

	es.Publish(UserRegisteredEvent, nil)
	time.Sleep(200 * time.Millisecond)

	if es.Stats()["panicked"] == 0 {
		t.Fatal("expected panicked > 0")
	}
}

// TestEventServiceQueueFull 验证队列满时丢弃而非阻塞
func TestEventServiceQueueFull(t *testing.T) {
	es := ProvideEventService()
	defer es.Close()

	// 慢 handler，制造积压
	block := make(chan struct{})
	es.Subscribe(OrderStatusChangedEvent, func(data interface{}) {
		<-block
	})
	defer close(block)

	done := make(chan struct{})
	go func() {
		// 远超队列容量（4096 * workers）
		for i := 0; i < 100000; i++ {
			es.Publish(OrderStatusChangedEvent, nil)
		}
		close(done)
	}()

	select {
	case <-done:
		// 未阻塞，符合预期
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked when queue full")
	}
}

// TestStateServiceConcurrent 验证状态存储并发安全
func TestStateServiceConcurrent(t *testing.T) {
	s := ProvideStateService()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			s.Set("counter", n)
		}(i)
		go func() {
			defer wg.Done()
			s.GetInt("counter")
			s.GetBool("flag")
			s.GetString("name")
		}()
	}
	wg.Wait()

	if _, ok := s.Get("counter"); !ok {
		t.Fatal("expected counter exists")
	}
}
