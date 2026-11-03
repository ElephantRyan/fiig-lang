// Shared load generator used against both the Go and Java HTTP spikes
// so the client side is identical for both measurements.
package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080/customer/", "base URL")
	duration := flag.Duration("duration", 60*time.Second, "test duration")
	concurrency := flag.Int("concurrency", 50, "concurrent workers")
	flag.Parse()

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency * 2,
		MaxConnsPerHost:     *concurrency * 2,
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	deadline := time.Now().Add(*duration)

	var mu sync.Mutex
	var latencies []time.Duration
	var errors int

	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for time.Now().Before(deadline) {
				start := time.Now()
				resp, err := client.Get(fmt.Sprintf("%s%d", *url, r.Intn(100000)))
				lat := time.Since(start)
				if err != nil {
					mu.Lock()
					errors++
					if errors <= 3 {
						fmt.Println("sample error:", err)
					}
					mu.Unlock()
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				mu.Lock()
				latencies = append(latencies, lat)
				mu.Unlock()
			}
		}(int64(w))
	}
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	n := len(latencies)
	pct := func(p float64) time.Duration {
		if n == 0 {
			return 0
		}
		idx := int(p * float64(n))
		if idx >= n {
			idx = n - 1
		}
		return latencies[idx]
	}

	var max time.Duration
	if n > 0 {
		max = latencies[n-1]
	}

	fmt.Printf("requests=%d errors=%d rps=%.1f p50=%s p99=%s max=%s\n",
		n, errors, float64(n)/duration.Seconds(), pct(0.50), pct(0.99), max)
}
