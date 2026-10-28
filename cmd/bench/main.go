// Command bench 是一个极简 HTTP 压测工具，用于验证服务的并发能力。
//
// 为什么不用 wrk/ab：容器与 CI 环境不一定装了这些工具，
// 用 Go 写一个可以跨平台直接跑，且能精确控制并发与连接复用。
//
// 用法：
//
//	go run ./cmd/bench -url http://127.0.0.1:8080/healthz -c 500 -n 50000
//	go run ./cmd/bench -url http://127.0.0.1:8080/api/ping -m POST -c 1000 -d 30s
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type result struct {
	latencies []time.Duration
	ok        atomic.Int64
	failed    atomic.Int64
	statuses  sync.Map // status code -> *atomic.Int64
}

func main() {
	var (
		url      = flag.String("url", "http://127.0.0.1:8080/healthz", "目标 URL")
		method   = flag.String("m", "GET", "HTTP 方法")
		body     = flag.String("body", "", "请求体")
		conc     = flag.Int("c", 200, "并发数")
		total    = flag.Int64("n", 0, "总请求数（与 -d 二选一）")
		duration = flag.Duration("d", 0, "压测时长（与 -n 二选一）")
		timeout  = flag.Duration("t", 10*time.Second, "单请求超时")
	)
	flag.Parse()

	if *total <= 0 && *duration <= 0 {
		*duration = 10 * time.Second
	}

	// 高并发压测需要足够的连接池，否则瓶颈在客户端
	tr := &http.Transport{
		MaxIdleConns:        *conc * 2,
		MaxIdleConnsPerHost: *conc * 2,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   *timeout,
		// 不跟随重定向，避免把 302 也计为成功
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C 提前结束并输出结果
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\n收到中断信号，输出当前结果...")
		cancel()
	}()

	res := &result{latencies: make([]time.Duration, 0, 4096)}
	var latMu sync.Mutex

	start := time.Now()
	var wg sync.WaitGroup
	var sent atomic.Int64

	worker := func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// 按总数控制
			if *total > 0 {
				if sent.Add(1) > *total {
					return
				}
			}

			var bodyReader io.Reader
			if *body != "" {
				bodyReader = stringReader(*body)
			}
			req, err := http.NewRequestWithContext(ctx, *method, *url, bodyReader)
			if err != nil {
				res.failed.Add(1)
				continue
			}
			if *body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			t0 := time.Now()
			resp, err := client.Do(req)
			elapsed := time.Since(t0)

			if err != nil {
				res.failed.Add(1)
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			latMu.Lock()
			res.latencies = append(res.latencies, elapsed)
			latMu.Unlock()

			v, _ := res.statuses.LoadOrStore(resp.StatusCode, new(atomic.Int64))
			v.(*atomic.Int64).Add(1)

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				res.ok.Add(1)
			} else {
				res.failed.Add(1)
			}
		}
	}

	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go worker()
	}

	if *duration > 0 {
		select {
		case <-time.After(*duration):
			cancel()
		case <-ctx.Done():
		}
	}

	wg.Wait()
	elapsed := time.Since(start)

	printReport(res, elapsed, *conc, &latMu)
}

// stringReader 避免依赖 strings.NewReader 的额外 import
type sr struct {
	s string
	i int
}

func stringReader(s string) io.Reader { return &sr{s: s} }

func (r *sr) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

func printReport(res *result, elapsed time.Duration, conc int, latMu *sync.Mutex) {
	total := res.ok.Load() + res.failed.Load()

	fmt.Println()
	fmt.Println("================ 压测结果 ================")
	fmt.Printf("总请求数 : %d\n", total)
	fmt.Printf("成功     : %d\n", res.ok.Load())
	fmt.Printf("失败     : %d\n", res.failed.Load())
	fmt.Printf("耗时     : %s\n", elapsed.Round(time.Millisecond))
	if elapsed > 0 {
		fmt.Printf("QPS      : %.0f\n", float64(total)/elapsed.Seconds())
	}
	fmt.Printf("并发     : %d\n", conc)

	fmt.Print("状态码   : ")
	var codes []int
	res.statuses.Range(func(k, v interface{}) bool {
		codes = append(codes, k.(int))
		return true
	})
	sort.Ints(codes)
	for _, c := range codes {
		v, _ := res.statuses.Load(c)
		fmt.Printf("%d=%d ", c, v.(*atomic.Int64).Load())
	}
	fmt.Println()

	latMu.Lock()
	lats := res.latencies
	latMu.Unlock()

	if len(lats) > 0 {
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		p := func(pct float64) time.Duration {
			idx := int(float64(len(lats)-1) * pct)
			return lats[idx]
		}
		fmt.Printf("延迟 P50 : %s\n", p(0.50).Round(time.Microsecond))
		fmt.Printf("延迟 P90 : %s\n", p(0.90).Round(time.Microsecond))
		fmt.Printf("延迟 P95 : %s\n", p(0.95).Round(time.Microsecond))
		fmt.Printf("延迟 P99 : %s\n", p(0.99).Round(time.Microsecond))
		fmt.Printf("延迟 Max : %s\n", lats[len(lats)-1].Round(time.Microsecond))
	}
	fmt.Println("==========================================")
}
