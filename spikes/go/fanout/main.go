package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	totalItems  = 10000
	concurrency = 50
	fakeLatency = 5 * time.Millisecond
)

func main() {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var active int64
	var peak int64

	start := time.Now()
	for i := 0; i < totalItems; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			n := atomic.AddInt64(&active, 1)
			for {
				p := atomic.LoadInt64(&peak)
				if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
					break
				}
			}

			time.Sleep(fakeLatency)
			atomic.AddInt64(&active, -1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Printf("items=%d concurrency=%d elapsed=%s peak_concurrent=%d heap_alloc_mb=%.2f\n",
		totalItems, concurrency, elapsed, peak, float64(mem.Alloc)/1024/1024)
}
