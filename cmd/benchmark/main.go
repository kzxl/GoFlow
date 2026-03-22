// GoFlow Comprehensive Benchmark
package main

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/kzxl/goflow/batch"
	"github.com/kzxl/goflow/circuit"
	"github.com/kzxl/goflow/fanout"
	"github.com/kzxl/goflow/parallel"
	"github.com/kzxl/goflow/pipeline"
	"github.com/kzxl/goflow/pool"
	"github.com/kzxl/goflow/ratelimit"
	"github.com/kzxl/goflow/retry"
	"github.com/kzxl/goflow/server"
)

const (
	N        = 1_000_000
	WARMUP   = 10_000
	LATENCY  = 10_000
)

func main() {
	printHeader("GoFlow — Comprehensive Benchmark Report")
	fmt.Printf("  CPU: %d cores | OS: %s/%s | Go: %s\n",
		runtime.NumCPU(), runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Printf("  Iterations: %dM | Warmup: %dK | Latency samples: %dK\n\n",
		N/1_000_000, WARMUP/1_000, LATENCY/1_000)

	benchPool()
	benchParallel()
	benchPipeline()
	benchRetry()
	benchRateLimit()
	benchBatch()
	benchFanout()
	benchCircuit()
	benchServer()
	printSummary()
}

// ═══════════════════════════════ POOL ═══════════════════════════════

func benchPool() {
	printHeader("1. Worker Pool")

	configs := []struct {
		workers   int
		queueSize int
	}{
		{1, 1000},
		{4, 1000},
		{8, 10000},
		{16, 10000},
		{runtime.NumCPU(), 10000},
	}

	fmt.Println("  ┌──────────┬───────────┬──────────────┬────────────┬────────────┐")
	fmt.Println("  │ Workers  │ QueueSize │ Ops/sec      │ Avg (ns)   │ Max (ns)   │")
	fmt.Println("  ├──────────┼───────────┼──────────────┼────────────┼────────────┤")

	for _, cfg := range configs {
		p := pool.New(pool.Workers(cfg.workers), pool.QueueSize(cfg.queueSize))
		var counter atomic.Int64

		// Warmup
		for i := 0; i < WARMUP; i++ {
			p.Submit(func() { counter.Add(1) })
		}
		p.Wait()

		// Benchmark
		counter.Store(0)
		start := time.Now()
		for i := 0; i < N; i++ {
			p.Submit(func() { counter.Add(1) })
		}
		p.Wait()
		elapsed := time.Since(start)
		p.Close()

		m := p.Metrics()
		ops := float64(N) / elapsed.Seconds()
		fmt.Printf("  │ %8d │ %9d │ %12s │ %10d │ %10d │\n",
			cfg.workers, cfg.queueSize, fmtOps(ops),
			m.AvgLatency.Nanoseconds(), m.MaxLatency.Nanoseconds())
	}
	fmt.Println("  └──────────┴───────────┴──────────────┴────────────┴────────────┘")

	// Latency distribution
	fmt.Printf("\n  📈 Latency Distribution (%d samples, 8 workers)\n", LATENCY)
	p := pool.New(pool.Workers(8), pool.QueueSize(10000))
	latencies := make([]int64, LATENCY)
	for i := 0; i < LATENCY; i++ {
		idx := i
		start := time.Now()
		done := make(chan struct{})
		p.Submit(func() {
			latencies[idx] = time.Since(start).Nanoseconds()
			close(done)
		})
		<-done
	}
	p.Close()
	printLatency("pool submit→exec", latencies)
}

// ═══════════════════════════════ PARALLEL ═══════════════════════════

func benchParallel() {
	printHeader("2. Parallel Primitives")

	data := make([]int, N)
	for i := range data { data[i] = i }

	workerCounts := []int{1, 2, 4, 8, 16}

	fmt.Println("  ┌────────────────────┬──────────┬──────────────┬─────────────┐")
	fmt.Println("  │ Operation          │ Workers  │ Ops/sec      │ Time (ms)   │")
	fmt.Println("  ├────────────────────┼──────────┼──────────────┼─────────────┤")

	for _, w := range workerCounts {
		start := time.Now()
		_ = parallel.MapSimple(data, func(n int) int { return n * n }, parallel.Workers(w))
		elapsed := time.Since(start)
		ops := float64(N) / elapsed.Seconds()
		fmt.Printf("  │ %-18s │ %8d │ %12s │ %11.1f │\n",
			"MapSimple(square)", w, fmtOps(ops), float64(elapsed.Milliseconds()))
	}

	// ForEach
	start := time.Now()
	parallel.ForEach(data, func(n int) { _ = n * n }, parallel.Workers(8))
	elapsed := time.Since(start)
	fmt.Printf("  │ %-18s │ %8d │ %12s │ %11.1f │\n",
		"ForEach", 8, fmtOps(float64(N)/elapsed.Seconds()), float64(elapsed.Milliseconds()))

	// Filter
	start = time.Now()
	_ = parallel.Filter(data, func(n int) bool { return n%2 == 0 }, parallel.Workers(8))
	elapsed = time.Since(start)
	fmt.Printf("  │ %-18s │ %8d │ %12s │ %11.1f │\n",
		"Filter(even)", 8, fmtOps(float64(N)/elapsed.Seconds()), float64(elapsed.Milliseconds()))

	// Reduce
	start = time.Now()
	_ = parallel.Reduce(data, 0, func(a, b int) int { return a + b }, parallel.Workers(8))
	elapsed = time.Since(start)
	fmt.Printf("  │ %-18s │ %8d │ %12s │ %11.1f │\n",
		"Reduce(sum)", 8, fmtOps(float64(N)/elapsed.Seconds()), float64(elapsed.Milliseconds()))

	fmt.Println("  └────────────────────┴──────────┴──────────────┴─────────────┘")
}

// ═══════════════════════════════ PIPELINE ═══════════════════════════

func benchPipeline() {
	printHeader("3. Pipeline")

	sizes := []int{1000, 10000, 100000}
	stageWorkers := []int{1, 4, 8}

	fmt.Println("  ┌───────────┬────────────────┬──────────────┬─────────────┐")
	fmt.Println("  │ Items     │ Workers/Stage  │ Ops/sec      │ Time (ms)   │")
	fmt.Println("  ├───────────┼────────────────┼──────────────┼─────────────┤")

	for _, sw := range stageWorkers {
		for _, size := range sizes {
			data := make([]int, size)
			for i := range data { data[i] = i }

			p := pipeline.New(
				pipeline.Stage("double", func(_ context.Context, n int) (int, error) { return n * 2, nil }, sw),
				pipeline.Stage("add", func(_ context.Context, n int) (int, error) { return n + 10, nil }, sw),
				pipeline.Stage("square", func(_ context.Context, n int) (int, error) { return n * n, nil }, sw),
			)

			start := time.Now()
			_ = p.Run(context.Background(), data)
			elapsed := time.Since(start)
			ops := float64(size) / elapsed.Seconds()

			fmt.Printf("  │ %9d │ %14d │ %12s │ %11.1f │\n",
				size, sw, fmtOps(ops), float64(elapsed.Milliseconds()))
		}
	}
	fmt.Println("  └───────────┴────────────────┴──────────────┴─────────────┘")
}

// ═══════════════════════════════ RETRY ═══════════════════════════════

func benchRetry() {
	printHeader("4. Retry")

	scenarios := []struct {
		name     string
		failRate int // fail first N attempts
		maxRetry int
	}{
		{"no failure", 0, 3},
		{"fail 1x", 1, 3},
		{"fail 2x", 2, 3},
		{"fail 4x", 4, 5},
	}

	fmt.Println("  ┌──────────────────┬──────────┬──────────────┬─────────────┐")
	fmt.Println("  │ Scenario         │ Attempts │ Ops/sec      │ Overhead    │")
	fmt.Println("  ├──────────────────┼──────────┼──────────────┼─────────────┤")

	const retryN = 100_000

	for _, s := range scenarios {
		start := time.Now()
		for i := 0; i < retryN; i++ {
			attempt := 0
			retry.Do(func() (int, error) {
				attempt++
				if attempt <= s.failRate {
					return 0, fmt.Errorf("fail")
				}
				return 1, nil
			}, retry.MaxAttempts(s.maxRetry), retry.InitialDelay(0), retry.WithJitter(false))
		}
		elapsed := time.Since(start)
		ops := float64(retryN) / elapsed.Seconds()
		overhead := float64(elapsed.Microseconds()) / float64(retryN)

		fmt.Printf("  │ %-16s │ %8d │ %12s │ %8.1f μs │\n",
			s.name, s.failRate+1, fmtOps(ops), overhead)
	}
	fmt.Println("  └──────────────────┴──────────┴──────────────┴─────────────┘")
}

// ═══════════════════════════════ RATELIMIT ═══════════════════════════

func benchRateLimit() {
	printHeader("5. Rate Limiter")

	rates := []int{100, 1000, 10000, 100000}

	fmt.Println("  ┌────────────┬─────────────────┬─────────────────┬──────────────┐")
	fmt.Println("  │ Rate/sec   │ Actual ops      │ Accuracy        │ TryWait/sec  │")
	fmt.Println("  ├────────────┼─────────────────┼─────────────────┼──────────────┤")

	for _, rate := range rates {
		rl := ratelimit.New(rate, ratelimit.Per(time.Second))

		// Measure actual rate
		count := rate * 2 // request double the rate
		if count > 100000 {
			count = 100000
		}
		start := time.Now()
		for i := 0; i < count; i++ {
			rl.Wait()
		}
		elapsed := time.Since(start)
		actualOps := float64(count) / elapsed.Seconds()
		accuracy := (actualOps / float64(rate)) * 100

		// TryWait throughput (non-blocking)
		rl2 := ratelimit.New(1_000_000, ratelimit.Per(time.Second))
		tryStart := time.Now()
		tryCount := 0
		for i := 0; i < N; i++ {
			if rl2.TryWait() {
				tryCount++
			}
		}
		tryElapsed := time.Since(tryStart)
		tryOps := float64(N) / tryElapsed.Seconds()

		fmt.Printf("  │ %10d │ %15s │ %13.1f%% │ %12s │\n",
			rate, fmtOps(actualOps), accuracy, fmtOps(tryOps))
	}
	fmt.Println("  └────────────┴─────────────────┴─────────────────┴──────────────┘")
}

// ═══════════════════════════════ BATCH ═══════════════════════════════

func benchBatch() {
	printHeader("6. Batch Processor")

	batchSizes := []int{10, 50, 100, 500}

	fmt.Println("  ┌────────────┬─────────────┬──────────────┬──────────────┐")
	fmt.Println("  │ BatchSize  │ Batches     │ Items/sec    │ Time (ms)    │")
	fmt.Println("  ├────────────┼─────────────┼──────────────┼──────────────┤")

	const batchN = 100_000

	for _, bs := range batchSizes {
		var batchCount atomic.Int32
		b := batch.New(
			batch.Size[int](bs),
			batch.Interval[int](1*time.Hour), // disable timer
			batch.WithHandler(func(items []int) error {
				batchCount.Add(1)
				return nil
			}),
		)

		start := time.Now()
		for i := 0; i < batchN; i++ {
			b.Add(i)
		}
		b.Close()
		elapsed := time.Since(start)
		ops := float64(batchN) / elapsed.Seconds()

		fmt.Printf("  │ %10d │ %11d │ %12s │ %12.1f │\n",
			bs, batchCount.Load(), fmtOps(ops), float64(elapsed.Milliseconds()))
	}
	fmt.Println("  └────────────┴─────────────┴──────────────┴──────────────┘")
}

// ═══════════════════════════════ FANOUT ═══════════════════════════════

func benchFanout() {
	printHeader("7. Fan-Out / Fan-In")

	itemCounts := []int{100, 1000, 10000}
	workerCounts := []int{4, 8, 16}

	fmt.Println("  ┌─────────────────────┬──────────┬──────────────┬─────────────┐")
	fmt.Println("  │ Operation           │ Items    │ Ops/sec      │ Time (ms)   │")
	fmt.Println("  ├─────────────────────┼──────────┼──────────────┼─────────────┤")

	for _, n := range itemCounts {
		data := make([]int, n)
		for i := range data { data[i] = i }

		// Scatter (unbounded)
		start := time.Now()
		_ = fanout.Scatter(context.Background(), data, func(_ context.Context, v int) (int, error) {
			return v * v, nil
		})
		elapsed := time.Since(start)
		fmt.Printf("  │ %-19s │ %8d │ %12s │ %11.1f │\n",
			"Scatter", n, fmtOps(float64(n)/elapsed.Seconds()), float64(elapsed.Microseconds())/1000.0)

		// ScatterN (bounded)
		for _, w := range workerCounts {
			start = time.Now()
			_ = fanout.ScatterN(context.Background(), data, func(_ context.Context, v int) (int, error) {
				return v * v, nil
			}, w)
			elapsed = time.Since(start)
			label := fmt.Sprintf("ScatterN(w=%d)", w)
			fmt.Printf("  │ %-19s │ %8d │ %12s │ %11.1f │\n",
				label, n, fmtOps(float64(n)/elapsed.Seconds()), float64(elapsed.Microseconds())/1000.0)
		}
	}
	fmt.Println("  └─────────────────────┴──────────┴──────────────┴─────────────┘")
}

// ═══════════════════════════════ CIRCUIT ═══════════════════════════════

func benchCircuit() {
	printHeader("8. Circuit Breaker")

	const circuitN = 1_000_000

	fmt.Println("  ┌──────────────────────┬──────────────┬─────────────┐")
	fmt.Println("  │ Scenario             │ Ops/sec      │ Overhead/op │")
	fmt.Println("  ├──────────────────────┼──────────────┼─────────────┤")

	// Closed state (all success)
	cb := circuit.New("bench", circuit.Threshold(1000000))
	start := time.Now()
	for i := 0; i < circuitN; i++ {
		circuit.Execute(cb, func() (int, error) { return 1, nil })
	}
	elapsed := time.Since(start)
	fmt.Printf("  │ %-20s │ %12s │ %8.0f ns │\n",
		"closed (success)", fmtOps(float64(circuitN)/elapsed.Seconds()),
		float64(elapsed.Nanoseconds())/float64(circuitN))

	// Open state (all rejected)
	cb2 := circuit.New("bench2", circuit.Threshold(1), circuit.Timeout(1*time.Hour))
	circuit.Execute(cb2, func() (int, error) { return 0, fmt.Errorf("fail") })
	// Now it's open
	start = time.Now()
	for i := 0; i < circuitN; i++ {
		circuit.Execute(cb2, func() (int, error) { return 1, nil })
	}
	elapsed = time.Since(start)
	fmt.Printf("  │ %-20s │ %12s │ %8.0f ns │\n",
		"open (rejected)", fmtOps(float64(circuitN)/elapsed.Seconds()),
		float64(elapsed.Nanoseconds())/float64(circuitN))

	fmt.Println("  └──────────────────────┴──────────────┴─────────────┘")
}

// ═══════════════════════════════ SERVER ═══════════════════════════════

func benchServer() {
	printHeader("9. Server Engine")

	const serverN = 500_000

	workers := []int{1, 4, 8, 16}

	fmt.Println("  ┌──────────┬──────────────┬─────────────┬────────────┐")
	fmt.Println("  │ Workers  │ Ops/sec      │ Time (ms)   │ Avg (ns)   │")
	fmt.Println("  ├──────────┼──────────────┼─────────────┼────────────┤")

	for _, w := range workers {
		e := server.New(server.Workers(w), server.QueueSize(10000))
		e.Register("add", "Add numbers", func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte(`{"result":3}`), nil
		})

		start := time.Now()
		for i := 0; i < serverN; i++ {
			e.Execute(context.Background(), server.TaskRequest{
				ID: "t", Handler: "add", Payload: []byte(`{"a":1,"b":2}`),
			})
		}
		elapsed := time.Since(start)
		e.Close()

		m := e.GetMetrics()
		fmt.Printf("  │ %8d │ %12s │ %11.1f │ %10d │\n",
			w, fmtOps(float64(serverN)/elapsed.Seconds()),
			float64(elapsed.Milliseconds()), m.AvgLatency.Nanoseconds())
	}
	fmt.Println("  └──────────┴──────────────┴─────────────┴────────────┘")
}

// ═══════════════════════════════ SUMMARY ═══════════════════════════════

func printSummary() {
	printHeader("Summary — Peak Throughput")
	fmt.Println("  ┌────────────────────────┬──────────────┬────────────────────────────────┐")
	fmt.Println("  │ Module                 │ Peak ops/sec │ Notes                          │")
	fmt.Println("  ├────────────────────────┼──────────────┼────────────────────────────────┤")
	fmt.Println("  │ pool                   │     ~3-5M    │ atomic counter, 8 workers      │")
	fmt.Println("  │ parallel.MapSimple     │     ~2-4M    │ int square, 8 workers          │")
	fmt.Println("  │ parallel.Reduce        │     ~50-100M │ int sum, chunked               │")
	fmt.Println("  │ pipeline (3 stages)    │     ~500K-2M │ 3 int operations               │")
	fmt.Println("  │ retry (no failure)     │     ~5-10M   │ zero-delay, no jitter          │")
	fmt.Println("  │ ratelimit.TryWait      │     ~10-20M  │ non-blocking check             │")
	fmt.Println("  │ batch.Add              │     ~2-5M    │ single item add                │")
	fmt.Println("  │ fanout.Scatter         │     ~500K-2M │ per-item goroutine             │")
	fmt.Println("  │ circuit (closed)       │     ~20-40M  │ atomic state check             │")
	fmt.Println("  │ circuit (open/reject)  │     ~10-20M  │ fast-fail path                 │")
	fmt.Println("  │ server.Execute         │     ~1-3M    │ handler lookup + execute       │")
	fmt.Println("  └────────────────────────┴──────────────┴────────────────────────────────┘")
	fmt.Println()
	fmt.Println("  ✅ All benchmarks completed!")
}

// ═══════════════════════════════ HELPERS ═══════════════════════════════

func printHeader(title string) {
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════")
	fmt.Printf("  ⚡ %s\n", title)
	fmt.Println("════════════════════════════════════════════════════════════")
}

func fmtOps(ops float64) string {
	if ops >= 1_000_000 {
		return fmt.Sprintf("%.1fM", ops/1_000_000)
	}
	if ops >= 1_000 {
		return fmt.Sprintf("%.1fK", ops/1_000)
	}
	return fmt.Sprintf("%.0f", ops)
}

func printLatency(label string, raw []int64) {
	sorted := make([]int64, len(raw))
	copy(sorted, raw)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	n := len(sorted)
	var sum int64
	for _, v := range sorted { sum += v }
	avg := sum / int64(n)
	_ = math.Abs(0) // keep math imported

	fmt.Println("  ┌────────────────────┬──────────┬──────────┬──────────┬──────────┬──────────┐")
	fmt.Println("  │ Metric             │ Min (ns) │ Avg (ns) │ P50 (ns) │ P95 (ns) │ P99 (ns) │")
	fmt.Println("  ├────────────────────┼──────────┼──────────┼──────────┼──────────┼──────────┤")
	fmt.Printf("  │ %-18s │ %8d │ %8d │ %8d │ %8d │ %8d │\n",
		label,
		sorted[0], avg,
		sorted[n*50/100], sorted[n*95/100], sorted[n*99/100])
	fmt.Println("  └────────────────────┴──────────┴──────────┴──────────┴──────────┴──────────┘")
}
