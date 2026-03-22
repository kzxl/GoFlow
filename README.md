# 🌊 GoFlow — Go Concurrency Toolkit

> **High-performance concurrency primitives for Go — worker pools, parallel processing, pipelines, and more.**

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

GoFlow provides **11 production-ready modules** for concurrent/parallel processing in Go, plus a **cross-language engine** that allows C#, Python, or any gRPC-compatible language to submit tasks for processing.

---

## 📦 Modules

| Module | Description | Key Feature |
|--------|-------------|-------------|
| [`pool`](#pool) | Goroutine worker pool (ants-inspired) | Worker reuse, Tune, Expiry purge |
| [`pool.PoolWithFunc`](#poolwithfunc) | Typed pool — fixed handler | Zero closure alloc, less GC |
| [`future`](#future) | Future/Promise pattern | Then, All, Race, GetTimeout |
| [`errgroup`](#errorgroup) | Error group with cancellation | First-error cancel, bounded |
| [`parallel`](#parallel) | Parallel Map/ForEach/Filter/Reduce | Generic, order-preserving |
| [`pipeline`](#pipeline) | Multi-stage processing pipeline | Per-stage concurrency |
| [`retry`](#retry) | Retry with exponential backoff | Jitter, custom predicates |
| [`ratelimit`](#ratelimit) | Token bucket rate limiter | WaitN, TryWait, context-aware |
| [`batch`](#batch) | Batch collector + processor | Auto-flush by size/interval |
| [`fanout`](#fanout) | Fan-out/Fan-in patterns | Scatter, Broadcast, FirstSuccess |
| [`circuit`](#circuit) | Circuit breaker (3-state) | Configurable threshold/timeout |
| [`server`](#server) | Cross-language task engine | gRPC ready, handler registry |

---

## 🚀 Quick Start

```bash
go get github.com/kzxl/goflow
```

```go
import (
    "github.com/kzxl/goflow/pool"      // worker pool
    "github.com/kzxl/goflow/future"    // async results
    "github.com/kzxl/goflow/errgroup"  // error group
    "github.com/kzxl/goflow/parallel"  // parallel map/filter/reduce
    "github.com/kzxl/goflow/pipeline"  // multi-stage pipeline
    "github.com/kzxl/goflow/retry"     // retry with backoff
)
```

---

## 📖 Module Documentation

### Pool

High-performance goroutine worker pool, inspired by [ants](https://github.com/panjf2000/ants) (13K★).

**Key design:** Worker goroutines are **reused** via `sync.Pool` + LIFO stack (no goroutine-per-task overhead). Idle workers are automatically purged by a scavenger goroutine.

```go
import "github.com/kzxl/goflow/pool"

// Create pool with 8 goroutines max
p, _ := pool.New(8)
defer p.Close()

// Unlimited pool
p, _ := pool.New(0)

// Non-blocking mode (returns error instead of blocking)
p, _ := pool.New(100, pool.Nonblocking())

// Submit tasks
for _, item := range items {
    p.Submit(func() { process(item) })
}
p.Wait()

// Dynamic resize at runtime
p.Tune(16)

// Metrics
m := p.Metrics()
fmt.Printf("Running: %d, Completed bringing: %d\n", m.Running, m.Completed)
```

ants-inspired features:
- **Worker reuse** — `sync.Pool` + LIFO stack, no goroutine-per-task overhead
- **Expiry purge** — scavenger goroutine removes idle workers (configurable `ExpiryDuration`)
- **sync.Cond blocking** — efficient wait/signal, no spin loops
- **Nonblocking mode** — `ErrPoolOverload` instead of blocking
- **MaxBlockingTasks** — limit goroutines waiting for workers  
- **Panic recovery** — per-task, worker continues after panic
- **Tune / Reboot** — resize at runtime, restart after Release
- **PreAlloc** — pre-allocate worker stack for predictable memory

---

### PoolWithFunc

Typed pool with a fixed handler — avoids closure allocation per task (less GC pressure).

```go
p, _ := pool.NewWithFunc(8, func(arg any) {
    item := arg.(MyItem)
    process(item)
})
defer p.Close()

for _, item := range items {
    p.Invoke(item)  // no closure created!
}
p.Wait()
```

---

### Future

Generic Future/Promise pattern for async results.

```go
import "github.com/kzxl/goflow/future"

f := future.Go(func() (int, error) { return expensiveCalc(), nil })
// ... do other work ...
result, err := f.Get()            // block until done
result, err := f.GetTimeout(5*s)  // with timeout

// Chain transformations
f2 := future.Then(f, func(n int) (string, error) {
    return fmt.Sprintf("result=%d", n), nil
})

// Wait for all / race
results, _ := future.All(f1, f2, f3)
winner, _ := future.Race(f1, f2)
```

---

### ErrorGroup

Parallel execution with first-error cancellation and bounded concurrency.

```go
import "github.com/kzxl/goflow/errgroup"

g, ctx := errgroup.New(errgroup.Workers(4))

for _, url := range urls {
    g.GoWithContext(func(ctx context.Context) error {
        return fetch(ctx, url)
    })
}

if err := g.Wait(); err != nil {
    log.Fatal("first error:", err)
}
```

---

### Parallel

High-level parallel processing primitives with Go generics.

```go
import "github.com/kzxl/goflow/parallel"

// Map: transform items in parallel (order preserved)
results, errs := parallel.Map(urls, func(url string) (Response, error) {
    return http.Get(url)
}, parallel.Workers(10))

// MapSimple: no error return
squares := parallel.MapSimple(numbers, func(n int) int {
    return n * n
}, parallel.Workers(4))

// ForEach: side effects
parallel.ForEach(files, func(f string) {
    processFile(f)
}, parallel.Workers(8))

// Filter: parallel predicate
largeFiles := parallel.Filter(files, func(f File) bool {
    return f.Size > 1_000_000
}, parallel.Workers(4))

// Reduce: parallel aggregation
total := parallel.Reduce(numbers, 0, func(a, b int) int {
    return a + b
}, parallel.Workers(4))
```

**Features:**
- Generic types — works with any type
- Order-preserving — results match input order
- Context cancellation — `parallel.WithContext(ctx)`
- Bounded concurrency — `parallel.Workers(n)`

---

### Pipeline

Multi-stage processing pipeline with per-stage concurrency.

```go
import "github.com/kzxl/goflow/pipeline"

// Define stages
p := pipeline.New(
    pipeline.Stage("download", func(ctx context.Context, url string) (string, error) {
        return httpGet(url)
    }, 10),  // 10 concurrent downloads

    pipeline.Stage("parse", func(ctx context.Context, html string) (string, error) {
        return extractData(html)
    }, 4),   // 4 concurrent parsers

    pipeline.Stage("save", func(ctx context.Context, data string) (string, error) {
        return saveToDB(data)
    }, 2),   // 2 concurrent writers
)

// Process batch
results := p.Run(ctx, urls)
for _, r := range results {
    if r.Err != nil {
        log.Printf("Error at stage %s: %v", r.Stage, r.Err)
    }
}

// Stream mode
resultCh := p.RunStream(ctx, inputChan)
for r := range resultCh {
    fmt.Println(r.Value)
}
```

**Features:**
- Per-stage worker count
- Error propagation with stage name
- Batch mode (`Run`) and stream mode (`RunStream`)
- Context cancellation

---

### Retry

Configurable retry with exponential backoff and jitter.

```go
import "github.com/kzxl/goflow/retry"

// Basic retry
result, err := retry.Do(func() (string, error) {
    return callUnstableAPI()
}, retry.MaxAttempts(5))

// Full configuration
result, err := retry.Do(func() (Response, error) {
    return httpPost(url, body)
},
    retry.MaxAttempts(5),
    retry.InitialDelay(100*time.Millisecond),
    retry.MaxDelay(10*time.Second),
    retry.Multiplier(2.0),
    retry.WithJitter(true),  // randomize delay
    retry.WithContext(ctx),  // cancel via context
    retry.RetryIf(func(err error) bool {
        return isTransient(err)  // only retry transient errors
    }),
    retry.OnRetry(func(attempt int, err error, delay time.Duration) {
        log.Printf("Retry #%d after %v: %v", attempt, delay, err)
    }),
)

// Simple (no return value)
err := retry.DoSimple(func() error {
    return sendEmail(msg)
}, retry.MaxAttempts(3))
```

**Features:**
- Exponential backoff with configurable multiplier
- Jitter to prevent thundering herd
- Custom retry predicate (`RetryIf`)
- Generic return type
- `RetryError` wraps last error with attempt count

---

### Ratelimit

Token bucket rate limiter for controlling throughput.

```go
import "github.com/kzxl/goflow/ratelimit"

// 100 operations per second
rl := ratelimit.New(100, ratelimit.Per(time.Second))

// Blocking wait for token
rl.Wait()
callAPI()

// Wait for N tokens
rl.WaitN(5)
batchProcess()

// Non-blocking
if rl.TryWait() {
    callAPI()
} else {
    log.Println("rate limited")
}

// With context cancellation
if err := rl.WaitWithContext(ctx); err != nil {
    return err
}

// Check available tokens
fmt.Println("Available:", rl.Available())
```

**Features:**
- Token bucket algorithm with smooth refill
- Blocking `Wait` / non-blocking `TryWait`
- Context-aware cancellation
- Configurable rate and time window

---

### Batch

Batch collector that groups items and processes them together.

```go
import "github.com/kzxl/goflow/batch"

// Auto-flush every 100 items or every 5 seconds
b := batch.New(
    batch.Size[Record](100),
    batch.Interval[Record](5*time.Second),
    batch.WithHandler(func(records []Record) error {
        return db.BulkInsert(records)
    }),
    batch.OnError(func(err error, records []Record) {
        log.Printf("Failed to insert %d records: %v", len(records), err)
    }),
)
defer b.Close()  // flush remaining + stop

// Add items (auto-batched)
for _, record := range records {
    b.Add(record)
}

// Or add multiple at once
b.AddAll(moreRecords)

// Force flush
b.Flush()

// Check stats
stats := b.GetStats()
fmt.Printf("Batches: %d, Items: %d\n", stats.BatchCount, stats.ItemCount)
```

**Features:**
- Auto-flush by size or time interval
- Thread-safe `Add`/`AddAll`
- Manual `Flush` and graceful `Close`
- Error callback with failed items
- Statistics tracking

---

### Fanout

Fan-out/Fan-in concurrency patterns.

```go
import "github.com/kzxl/goflow/fanout"

// Scatter: process each item on its own goroutine
results := fanout.Scatter(ctx, urls, func(ctx context.Context, url string) (Response, error) {
    return httpGet(url)
})

// ScatterN: bounded fan-out (max N goroutines)
results := fanout.ScatterN(ctx, urls, fetchFunc, 10)

// Broadcast: same input → multiple handlers
results := fanout.Broadcast(ctx, request,
    callServiceA,
    callServiceB,
    callServiceC,
)

// Gather: merge multiple channels into one
merged := fanout.Gather(ch1, ch2, ch3)
for item := range merged {
    process(item)
}

// FirstSuccess: race multiple strategies, return first success
result, err := fanout.FirstSuccess(ctx,
    func(ctx context.Context) (string, error) { return cache.Get(key) },
    func(ctx context.Context) (string, error) { return db.Get(key) },
    func(ctx context.Context) (string, error) { return api.Get(key) },
)
```

**Features:**
- `Scatter` — unbounded parallel (1 goroutine per item)
- `ScatterN` — bounded parallel (max N goroutines)
- `Broadcast` — same input to multiple functions
- `Gather` — merge channels
- `FirstSuccess` — racing with cancellation

---

### Circuit

Circuit breaker for protecting external services.

```
Closed (normal) → failures reach threshold → Open (reject all)
    ↑                                              ↓
    └────────── Half-Open (test 1 request) ←── timeout expires
```

```go
import "github.com/kzxl/goflow/circuit"

cb := circuit.New("payment-api",
    circuit.Threshold(5),               // open after 5 failures
    circuit.Timeout(30*time.Second),     // try again after 30s
    circuit.HalfOpenMax(1),              // allow 1 test request
    circuit.OnStateChange(func(name string, from, to circuit.State) {
        log.Printf("Circuit %s: %s → %s", name, from, to)
    }),
)

// Use the breaker
result, err := circuit.Execute(cb, func() (Response, error) {
    return callPaymentAPI()
})

if errors.Is(err, circuit.ErrCircuitOpen) {
    // Circuit is open — use fallback
    return cachedResult, nil
}

// Check state
fmt.Println("State:", cb.State())   // CLOSED, OPEN, or HALF_OPEN

// Get metrics
m := cb.GetMetrics()
fmt.Printf("Requests: %d, Failures: %d\n", m.Requests, m.Failures)

// Manual reset
cb.Reset()
```

**Features:**
- Three states: Closed → Open → HalfOpen → Closed
- Configurable failure threshold and timeout
- Generic `Execute` with automatic state transitions
- State change callbacks
- Metrics tracking
- Manual reset

---

### Server

Cross-language task processing engine for submitting tasks from C#/Python/etc.

```go
import "github.com/kzxl/goflow/server"

// Create engine
engine := server.New(server.Workers(8), server.QueueSize(10000))

// Register handlers
engine.Register("resize-image", "Resize images", func(ctx context.Context, payload []byte) ([]byte, error) {
    var req ResizeRequest
    server.UnmarshalPayload(payload, &req)

    result := resize(req.Path, req.Width, req.Height)
    return server.MarshalPayload(result), nil
})

engine.Register("send-email", "Send emails", func(ctx context.Context, payload []byte) ([]byte, error) {
    var req EmailRequest
    server.UnmarshalPayload(payload, &req)
    return nil, smtp.Send(req.To, req.Subject, req.Body)
})

// Execute single task
result := engine.Execute(ctx, server.TaskRequest{
    ID:      "task-1",
    Handler: "resize-image",
    Payload: server.MarshalPayload(ResizeRequest{Path: "photo.jpg", Width: 800}),
})

// Execute batch
results := engine.ExecuteBatch(ctx, tasks)

// Async execution
resultCh := engine.ExecuteAsync(ctx, task)
result := <-resultCh
```

**gRPC Integration:**

The `proto/goflow.proto` defines the cross-language contract. Generate client stubs for:
- **C#**: `dotnet-grpc` or `Grpc.Tools`
- **Python**: `grpcio-tools`
- **Java**: `protoc` with grpc-java

---

## ⚡ Performance Benchmark

> Run `go run ./cmd/benchmark` to reproduce. Test: 1M iterations, 10K warmup.

### Peak Throughput (All Modules)

| Module | Ops/sec | Overhead/op | Notes |
|--------|:-------:|:-----------:|-------|
| **circuit** (closed) | **32.2M** | 31 ns | Atomic state check |
| **batch.Add** | **94.6M** | ~10 ns | Batch size 100 |
| **ratelimit.TryWait** | **43.9M** | ~23 ns | Non-blocking check |
| **server.Execute** | **13.4M** | ~75 ns | Handler lookup + exec |
| **retry** (no fail) | **12.1M** | ~83 ns | Zero-delay, no jitter |
| **pool** (8 workers) | **3.4M** | ~294 ns | Atomic counter task |
| **parallel.Map** (8w) | **2.0M** | ~500 ns | Int square, order-preserved |
| **fanout.Scatter** (10K) | **3.8M** | ~263 ns | Per-item goroutine |
| **pipeline** (3 stages) | **1.1M** | ~909 ns | 8 workers/stage |

### Worker Pool Scaling

| Workers | Ops/sec | Avg Latency |
|:-------:|:-------:|:-----------:|
| 1 | 2.2M | 14 ns |
| 4 | 3.4M | 1 ns |
| 8 | 3.4M | 0 ns |
| 16 | 3.7M | 0 ns |

### Pool Latency Distribution (10K samples)

| Min | Avg | P50 | P95 | P99 |
|:---:|:---:|:---:|:---:|:---:|
| 0 ns | 337 ns | 300 ns | 200 ns | 4,200 ns |

### Parallel Scaling (1M items)

| Operation | 1 worker | 4 workers | 8 workers | 16 workers |
|-----------|:--------:|:---------:|:---------:|:----------:|
| MapSimple | 5.6M | 2.3M | 2.0M | 1.2M |
| ForEach | - | - | 2.2M | - |
| Filter | - | - | 1.2M | - |
| Reduce | - | - | 62.6M | - |

### Circuit Breaker Overhead

| State | Ops/sec | Overhead/op |
|-------|:-------:|:-----------:|
| Closed (pass-through) | **32.2M** | **31 ns** |
| Open (fast-reject) | **2.5M** | **400 ns** |

### Why Go?

| Feature | Go Goroutine | OS Thread (C#/Java) |
|---------|:------------:|:-------------------:|
| Stack size | **2 KB** | 1 MB |
| Creation cost | **~1 μs** | ~100 μs |
| Max concurrent | **1,000,000+** | ~10,000 |
| Context switching | **~200 ns** | ~1-2 μs |

GoFlow leverages goroutines to achieve **massive concurrency** with minimal overhead.

---

## 🏗️ Project Structure

```
GoFlow/
├── pool/           Worker pool (bounded concurrency + metrics)
├── parallel/       Parallel Map/ForEach/Filter/Reduce (generics)
├── pipeline/       Multi-stage pipeline (per-stage workers)
├── retry/          Retry with exponential backoff + jitter
├── ratelimit/      Token bucket rate limiter
├── batch/          Batch collector (auto-flush by size/interval)
├── fanout/         Fan-out/Fan-in (Scatter/Broadcast/Gather)
├── circuit/        Circuit breaker (3-state machine)
├── server/         Cross-language task engine
├── proto/          gRPC protocol definition
└── cmd/demo/       Demo application
```

---

## 🔌 Cross-Language Integration

GoFlow can act as a **high-performance backend engine** for any language via gRPC:

```
┌──────────┐     gRPC      ┌──────────────┐
│  C# App  │ ──────────→   │              │
└──────────┘                │   GoFlow     │
┌──────────┐     gRPC      │   Engine     │
│  Python  │ ──────────→   │              │
└──────────┘                │  goroutines  │
┌──────────┐     gRPC      │  worker pool │
│  Node.js │ ──────────→   │              │
└──────────┘                └──────────────┘
```

### C# Client Example

```csharp
// Generated from proto/goflow.proto
var client = new GoFlowService.GoFlowServiceClient(channel);

var result = await client.ExecuteAsync(new TaskRequest {
    Id = Guid.NewGuid().ToString(),
    Handler = "resize-image",
    Payload = JsonSerializer.SerializeToUtf8Bytes(new { Path = "photo.jpg", Width = 800 }),
    TimeoutMs = 5000
});

if (result.Success)
    Console.WriteLine($"Done in {result.DurationNs / 1_000_000}ms");
```

---

## 🎯 Real-World Use Cases

| Scenario | GoFlow Modules |
|----------|---------------|
| **Batch image processing** | `parallel.Map` + `ratelimit` |
| **Web scraping 10K URLs** | `pool` + `ratelimit` + `retry` |
| **ETL pipeline** | `pipeline` (extract→transform→load) |
| **Microservice calls** | `circuit` + `retry` + `fanout` |
| **Bulk email sending** | `batch` + `ratelimit` |
| **Multi-DB query** | `fanout.Scatter` |
| **File processing** | `parallel.ForEach` + `pool` |
| **API gateway** | `circuit` + `ratelimit` + `retry` |

---

## 📄 License

Apache License 2.0 — See [LICENSE](LICENSE) for details.

---

<p align="center">
  <strong>🌊 Flow with the Go — concurrency made simple.</strong>
</p>
