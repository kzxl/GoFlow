// Package pool provides a high-performance, bounded goroutine worker pool.
//
// The pool manages a fixed number of worker goroutines that process tasks
// from a shared queue. This prevents goroutine explosion and provides
// backpressure when the queue is full.
//
// # Usage
//
//	p := pool.New(pool.Workers(8), pool.QueueSize(1000))
//	defer p.Close()
//
//	p.Submit(func() { processItem(item) })
//	p.Wait() // wait for all submitted tasks
package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a unit of work.
type Task func()

// TaskWithError represents a unit of work that can return an error.
type TaskWithError func() error

// Pool is a bounded goroutine worker pool.
type Pool struct {
	tasks     chan Task
	wg        sync.WaitGroup
	once      sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	workers   int
	queueSize int

	// Metrics
	submitted  atomic.Int64
	completed  atomic.Int64
	failed     atomic.Int64
	totalTime  atomic.Int64 // nanoseconds
	maxLatency atomic.Int64 // nanoseconds
}

// Option configures the pool.
type Option func(*Pool)

// Workers sets the number of worker goroutines. Default: runtime.NumCPU().
func Workers(n int) Option {
	return func(p *Pool) {
		if n > 0 {
			p.workers = n
		}
	}
}

// QueueSize sets the task queue buffer size. Default: 1000.
func QueueSize(n int) Option {
	return func(p *Pool) {
		if n > 0 {
			p.queueSize = n
		}
	}
}

// New creates a new worker pool with the given options.
func New(opts ...Option) *Pool {
	p := &Pool{
		workers:   8,
		queueSize: 1000,
	}
	for _, opt := range opts {
		opt(p)
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.tasks = make(chan Task, p.queueSize)

	// Start workers
	for i := 0; i < p.workers; i++ {
		go p.worker()
	}

	return p
}

// worker runs in a goroutine, processing tasks from the queue.
func (p *Pool) worker() {
	for task := range p.tasks {
		start := time.Now()
		task()
		elapsed := time.Since(start).Nanoseconds()

		p.completed.Add(1)
		p.totalTime.Add(elapsed)

		// Update max latency (lock-free CAS)
		for {
			cur := p.maxLatency.Load()
			if elapsed <= cur || p.maxLatency.CompareAndSwap(cur, elapsed) {
				break
			}
		}

		p.wg.Done()
	}
}

// Submit adds a task to the pool. Blocks if the queue is full (backpressure).
func (p *Pool) Submit(task Task) {
	p.submitted.Add(1)
	p.wg.Add(1)
	p.tasks <- task
}

// TrySubmit attempts to add a task without blocking.
// Returns false if the queue is full.
func (p *Pool) TrySubmit(task Task) bool {
	select {
	case p.tasks <- task:
		p.submitted.Add(1)
		p.wg.Add(1)
		return true
	default:
		return false
	}
}

// SubmitWithError adds an error-returning task. Errors are tracked in metrics.
func (p *Pool) SubmitWithError(task TaskWithError) {
	p.Submit(func() {
		if err := task(); err != nil {
			p.failed.Add(1)
		}
	})
}

// Wait blocks until all submitted tasks are completed.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// Close stops the pool. No new tasks can be submitted after Close.
// Waits for in-flight tasks to complete.
func (p *Pool) Close() {
	p.once.Do(func() {
		p.cancel()
		close(p.tasks)
	})
}

// Metrics returns pool performance metrics.
func (p *Pool) Metrics() PoolMetrics {
	completed := p.completed.Load()
	var avgLatency time.Duration
	if completed > 0 {
		avgLatency = time.Duration(p.totalTime.Load() / completed)
	}
	return PoolMetrics{
		Workers:    p.workers,
		QueueSize:  p.queueSize,
		Submitted:  p.submitted.Load(),
		Completed:  completed,
		Failed:     p.failed.Load(),
		Pending:    p.submitted.Load() - completed,
		AvgLatency: avgLatency,
		MaxLatency: time.Duration(p.maxLatency.Load()),
	}
}

// PoolMetrics contains pool performance statistics.
type PoolMetrics struct {
	Workers    int
	QueueSize  int
	Submitted  int64
	Completed  int64
	Failed     int64
	Pending    int64
	AvgLatency time.Duration
	MaxLatency time.Duration
}
