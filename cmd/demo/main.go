// GoFlow Demo — demonstrates all 8 modules
package main

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
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
)

func main() {
	printHeader("GoFlow — Go Concurrency Toolkit Demo")

	demoPool()
	demoParallel()
	demoPipeline()
	demoRetry()
	demoRateLimit()
	demoBatch()
	demoFanout()
	demoCircuit()
	demoBenchmark()

	fmt.Println("\n  ✅ All demos completed!")
}

func demoPool() {
	printHeader("1. Worker Pool")

	p := pool.New(pool.Workers(4), pool.QueueSize(100))

	var sum atomic.Int64
	for i := 1; i <= 100; i++ {
		n := i
		p.Submit(func() {
			sum.Add(int64(n))
		})
	}
	p.Wait()
	p.Close()

	m := p.Metrics()
	fmt.Printf("  Sum 1..100 = %d (expected: 5050)\n", sum.Load())
	fmt.Printf("  Pool: %d workers, %d completed, avg latency: %v\n",
		m.Workers, m.Completed, m.AvgLatency)
}

func demoParallel() {
	printHeader("2. Parallel Map/Filter/Reduce")

	// Map: square numbers
	nums := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	squares := parallel.MapSimple(nums, func(n int) int {
		return n * n
	}, parallel.Workers(4))
	fmt.Printf("  Squares: %v\n", squares)

	// Filter: even numbers
	evens := parallel.Filter(nums, func(n int) bool {
		return n%2 == 0
	}, parallel.Workers(4))
	fmt.Printf("  Evens:   %v\n", evens)

	// Reduce: sum
	total := parallel.Reduce(nums, 0, func(a, b int) int {
		return a + b
	}, parallel.Workers(4))
	fmt.Printf("  Sum:     %d\n", total)

	// Map with error handling
	results, errs := parallel.Map([]string{"10", "abc", "30"}, func(s string) (int, error) {
		return strconv.Atoi(s)
	}, parallel.Workers(2))
	fmt.Printf("  Parse results: %v, errors: %v\n", results, errs)
}

func demoPipeline() {
	printHeader("3. Pipeline (Multi-Stage)")

	p := pipeline.New(
		pipeline.Stage("double", func(_ context.Context, n int) (int, error) {
			return n * 2, nil
		}, 2),
		pipeline.Stage("add_10", func(_ context.Context, n int) (int, error) {
			return n + 10, nil
		}, 2),
		pipeline.Stage("to_string", func(_ context.Context, n int) (int, error) {
			// Just demonstrate, keep as int
			return n, nil
		}, 1),
	)

	input := []int{1, 2, 3, 4, 5}
	results := p.Run(context.Background(), input)

	for i, r := range results {
		if r.Err != nil {
			fmt.Printf("  [%d] Error at %s: %v\n", i, r.Stage, r.Err)
		} else {
			fmt.Printf("  [%d] %d → %d\n", i, input[i], r.Value)
		}
	}
}

func demoRetry() {
	printHeader("4. Retry with Backoff")

	attempt := 0
	result, err := retry.Do(func() (string, error) {
		attempt++
		if attempt < 3 {
			return "", fmt.Errorf("attempt %d failed", attempt)
		}
		return "success!", nil
	},
		retry.MaxAttempts(5),
		retry.InitialDelay(10*time.Millisecond),
		retry.OnRetry(func(a int, err error, d time.Duration) {
			fmt.Printf("  Retry #%d after %v: %v\n", a, d, err)
		}),
	)

	fmt.Printf("  Result: %q, Error: %v (took %d attempts)\n", result, err, attempt)
}

func demoRateLimit() {
	printHeader("5. Rate Limiter")

	rl := ratelimit.New(10, ratelimit.Per(time.Second)) // 10 ops/sec

	start := time.Now()
	for i := 0; i < 15; i++ {
		rl.Wait()
	}
	elapsed := time.Since(start)

	fmt.Printf("  15 operations at 10/sec took %v (expected ~1.5s)\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Available tokens: %d\n", rl.Available())
}

func demoBatch() {
	printHeader("6. Batch Processor")

	var batches atomic.Int32
	var totalItems atomic.Int32

	b := batch.New(
		batch.Size[string](5),
		batch.Interval[string](1*time.Second),
		batch.WithHandler(func(items []string) error {
			batches.Add(1)
			totalItems.Add(int32(len(items)))
			fmt.Printf("  Batch #%d: [%s] (%d items)\n",
				batches.Load(), strings.Join(items, ", "), len(items))
			return nil
		}),
	)

	for i := 1; i <= 12; i++ {
		b.Add(fmt.Sprintf("item_%d", i))
	}
	b.Close()

	stats := b.GetStats()
	fmt.Printf("  Stats: %d batches, %d items processed\n", stats.BatchCount, stats.ItemCount)
}

func demoFanout() {
	printHeader("7. Fan-Out / Fan-In")

	// Scatter: process items concurrently
	items := []int{10, 20, 30, 40, 50}
	results := fanout.Scatter(context.Background(), items, func(_ context.Context, n int) (int, error) {
		time.Sleep(10 * time.Millisecond) // simulate work
		return n * n, nil
	})

	fmt.Print("  Scatter squares: ")
	for _, r := range results {
		fmt.Printf("%d ", r.Value)
	}
	fmt.Println()

	// Broadcast: same input → multiple processors
	bResults := fanout.Broadcast(context.Background(), "hello",
		func(_ context.Context, s string) (string, error) {
			return strings.ToUpper(s), nil
		},
		func(_ context.Context, s string) (string, error) {
			return fmt.Sprintf("len=%d", len(s)), nil
		},
		func(_ context.Context, s string) (string, error) {
			return strings.Repeat(s, 2), nil
		},
	)

	fmt.Print("  Broadcast 'hello': ")
	for _, r := range bResults {
		fmt.Printf("[%s] ", r.Value)
	}
	fmt.Println()
}

func demoCircuit() {
	printHeader("8. Circuit Breaker")

	cb := circuit.New("unstable-api",
		circuit.Threshold(3),
		circuit.Timeout(500*time.Millisecond),
		circuit.OnStateChange(func(name string, from, to circuit.State) {
			fmt.Printf("  ⚡ %s: %s → %s\n", name, from, to)
		}),
	)

	// Simulate failures
	for i := 1; i <= 5; i++ {
		result, err := circuit.Execute(cb, func() (string, error) {
			if rand.Float64() < 0.8 { // 80% failure rate
				return "", fmt.Errorf("api error")
			}
			return "ok", nil
		})
		fmt.Printf("  Attempt %d: result=%q err=%v state=%s\n",
			i, result, err, cb.State())
	}

	// Wait for half-open
	time.Sleep(600 * time.Millisecond)

	result, err := circuit.Execute(cb, func() (string, error) {
		return "recovered!", nil
	})
	fmt.Printf("  After timeout: result=%q err=%v state=%s\n", result, err, cb.State())

	m := cb.GetMetrics()
	fmt.Printf("  Metrics: %d requests, %d successes, %d failures, %d rejected\n",
		m.Requests, m.Successes, m.Failures, m.Rejected)
}

func demoBenchmark() {
	printHeader("Performance Benchmark")

	const N = 1_000_000

	// Pool throughput
	start := time.Now()
	p := pool.New(pool.Workers(8), pool.QueueSize(10000))
	var counter atomic.Int64
	for i := 0; i < N; i++ {
		p.Submit(func() { counter.Add(1) })
	}
	p.Wait()
	p.Close()
	poolElapsed := time.Since(start)

	// Parallel Map throughput
	data := make([]int, N)
	for i := range data {
		data[i] = i
	}
	start = time.Now()
	_ = parallel.MapSimple(data, func(n int) int { return n * n }, parallel.Workers(8))
	mapElapsed := time.Since(start)

	fmt.Printf("  Pool:         %d tasks in %v → %s ops/sec\n", N, poolElapsed, formatOps(N, poolElapsed))
	fmt.Printf("  Parallel Map: %d items in %v → %s ops/sec\n", N, mapElapsed, formatOps(N, mapElapsed))
}

func printHeader(title string) {
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════")
	fmt.Printf("  🌊 %s\n", title)
	fmt.Println("════════════════════════════════════════════════════════════")
}

func formatOps(count int, elapsed time.Duration) string {
	ops := float64(count) / elapsed.Seconds()
	if ops >= 1_000_000 {
		return fmt.Sprintf("%.1fM", ops/1_000_000)
	}
	return fmt.Sprintf("%.0fK", ops/1_000)
}
