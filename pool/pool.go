// Package pool provides a high-performance, production-grade goroutine worker pool.
//
// Design inspired by ants (13K★) with GoFlow-specific enhancements.
//
// Key features:
//   - Worker goroutine reuse via sync.Pool (no goroutine-per-task overhead)
//   - Automatic expiry purge of idle workers (scavenger goroutine)
//   - sync.Cond based blocking (efficient wait/signal, no spin)
//   - Nonblocking mode for latency-sensitive code
//   - MaxBlockingTasks to limit goroutines waiting for workers
//   - Panic recovery with configurable handler
//   - Lock-free metrics via atomic counters
//   - Dynamic resize at runtime
//   - PreAlloc option for memory pre-allocation
//
// # Usage
//
//	p, _ := pool.New(1000)                     // pool with 1000 goroutines
//	p, _ := pool.New(0)                        // unlimited pool
//	p, _ := pool.New(100, pool.Nonblocking())  // non-blocking
//	defer p.Release()
//
//	err := p.Submit(func() { processItem(item) })
//	p.Tune(200)  // resize to 200 workers
package pool

import (
	"errors"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Sentinel errors
var (
	ErrPoolClosed    = errors.New("pool is closed")
	ErrPoolOverload  = errors.New("pool is overloaded (too many goroutines blocked)")
	ErrInvalidSize   = errors.New("invalid pool capacity, should be positive or zero (unlimited)")
)

// PanicHandler handles panics recovered from worker goroutines.
type PanicHandler func(recovered any)

// Logger interface for custom logging.
type Logger interface {
	Printf(format string, args ...any)
}

// ─── Worker ───────────────────────────────────────────────────────

// worker represents a reusable goroutine that processes tasks.
type worker struct {
	pool     *Pool
	task     chan func()
	lastUsed atomic.Int64 // unix nano
}

// run starts the worker goroutine loop.
func (w *worker) run() {
	w.pool.addRunning(1)
	go func() {
		defer func() {
			w.pool.addRunning(-1)
			w.pool.workerCache.Put(w)
			// Signal waiting submitters
			w.pool.cond.Signal()
		}()

		for fn := range w.task {
			if fn == nil {
				return // exit signal
			}

			// Execute task with panic recovery
			start := time.Now()
			func() {
				defer func() {
					if r := recover(); r != nil {
						w.pool.panicked.Add(1)
						if w.pool.panicHandler != nil {
							w.pool.panicHandler(r)
						} else if w.pool.logger != nil {
							w.pool.logger.Printf("goflow/pool: worker panic: %v\n%s\n", r, debug.Stack())
						}
					}
				}()
				fn()
			}()

			elapsed := time.Since(start).Nanoseconds()
			w.pool.completed.Add(1)
			w.pool.totalTime.Add(elapsed)

			// Update max latency (lock-free CAS)
			for {
				cur := w.pool.maxLatency.Load()
				if elapsed <= cur || w.pool.maxLatency.CompareAndSwap(cur, elapsed) {
					break
				}
			}

			// CRITICAL: always mark task done before reverting
			w.pool.taskWg.Done()

			// Return worker to the pool for reuse
			if ok := w.pool.revertWorker(w); !ok {
				return
			}
		}
	}()
}

// ─── Worker Stack ─────────────────────────────────────────────────

// workerStack is a LIFO stack for worker reuse, with expiry support.
type workerStack struct {
	items  []*worker
	expiry []*worker
}

func newWorkerStack(size int) *workerStack {
	return &workerStack{
		items: make([]*worker, 0, size),
	}
}

func (ws *workerStack) len() int {
	return len(ws.items)
}

func (ws *workerStack) isEmpty() bool {
	return len(ws.items) == 0
}

// push returns a worker to the stack.
func (ws *workerStack) push(w *worker) {
	ws.items = append(ws.items, w)
}

// pop retrieves the most recently used worker (LIFO — cache friendly).
func (ws *workerStack) pop() *worker {
	n := len(ws.items)
	if n == 0 {
		return nil
	}
	w := ws.items[n-1]
	ws.items[n-1] = nil // avoid memory leak
	ws.items = ws.items[:n-1]
	return w
}

// refresh removes expired workers via binary search.
func (ws *workerStack) refresh(duration time.Duration) []*worker {
	n := len(ws.items)
	if n == 0 {
		return nil
	}

	expiryTime := time.Now().Add(-duration).UnixNano()

	// Binary search for first non-expired worker
	lo, hi := 0, n-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if expiryTime < ws.items[mid].lastUsed.Load() {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	// hi is the index of the last expired worker
	index := hi

	ws.expiry = ws.expiry[:0]
	if index >= 0 {
		ws.expiry = append(ws.expiry, ws.items[:index+1]...)
		m := copy(ws.items, ws.items[index+1:])
		for i := m; i < n; i++ {
			ws.items[i] = nil
		}
		ws.items = ws.items[:m]
	}

	return ws.expiry
}

// reset shuts down all workers in the stack.
func (ws *workerStack) reset() {
	for i, w := range ws.items {
		w.task <- nil // exit signal
		ws.items[i] = nil
	}
	ws.items = ws.items[:0]
}

// ─── Pool ─────────────────────────────────────────────────────────

// Pool is a high-performance goroutine pool.
type Pool struct {
	// Configuration
	capacity       atomic.Int32  // 0 = unlimited
	expiryDuration time.Duration
	nonblocking    bool
	maxBlocking    int
	preAlloc       bool
	panicHandler   PanicHandler
	logger         Logger

	// State
	running  atomic.Int32
	closed   atomic.Bool
	blocking atomic.Int32

	// Worker management
	workers    *workerStack
	workerCache sync.Pool
	lock       sync.Locker
	cond       *sync.Cond

	// Lifecycle
	purgeDone chan struct{}
	stopPurge chan struct{}
	allDone   chan struct{}
	once      sync.Once

	// Wait group for submitted tasks
	taskWg sync.WaitGroup

	// Metrics (lock-free)
	submitted atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
	panicked  atomic.Int64
	totalTime atomic.Int64 // nanoseconds
	maxLatency atomic.Int64 // nanoseconds
}

// Option configures the pool.
type Option func(*Pool)

// Workers sets the pool capacity. 0 = unlimited.
func Workers(n int) Option {
	return func(p *Pool) { p.capacity.Store(int32(n)) }
}

// ExpiryDuration sets how long idle workers live before being purged.
// Default: 1 second. Set 0 to disable purging.
func ExpiryDuration(d time.Duration) Option {
	return func(p *Pool) { p.expiryDuration = d }
}

// Nonblocking makes Submit return ErrPoolOverload instead of blocking.
func Nonblocking() Option {
	return func(p *Pool) { p.nonblocking = true }
}

// MaxBlockingTasks limits how many goroutines can block on Submit.
// 0 = no limit.
func MaxBlockingTasks(n int) Option {
	return func(p *Pool) { p.maxBlocking = n }
}

// PreAlloc pre-allocates the worker stack.
func PreAlloc() Option {
	return func(p *Pool) { p.preAlloc = true }
}

// OnPanic sets the panic handler for worker goroutines.
func OnPanic(handler PanicHandler) Option {
	return func(p *Pool) { p.panicHandler = handler }
}

// WithLogger sets a custom logger.
func WithLogger(l Logger) Option {
	return func(p *Pool) { p.logger = l }
}

// QueueSize is kept for backwards compatibility (maps to capacity).
func QueueSize(n int) Option {
	return Workers(n)
}

// New creates a new goroutine pool.
// capacity: max number of concurrent goroutines. 0 = unlimited.
func New(capacity int, opts ...Option) (*Pool, error) {
	if capacity < 0 {
		return nil, ErrInvalidSize
	}

	p := &Pool{
		expiryDuration: time.Second,
		purgeDone:      make(chan struct{}),
		stopPurge:      make(chan struct{}),
		allDone:        make(chan struct{}),
	}
	p.capacity.Store(int32(capacity))

	for _, opt := range opts {
		opt(p)
	}

	// Spin lock for high contention scenarios
	p.lock = &sync.Mutex{}
	p.cond = sync.NewCond(p.lock)

	if p.preAlloc && capacity > 0 {
		p.workers = newWorkerStack(capacity)
	} else {
		p.workers = newWorkerStack(0)
	}

	// Worker cache for goroutine reuse
	p.workerCache.New = func() any {
		return &worker{
			pool: p,
			task: make(chan func(), 1),
		}
	}

	// Start scavenger goroutine
	if p.expiryDuration > 0 {
		go p.purgeStaleWorkers()
	}

	return p, nil
}

// purgeStaleWorkers periodically removes idle workers.
func (p *Pool) purgeStaleWorkers() {
	ticker := time.NewTicker(p.expiryDuration)
	defer func() {
		ticker.Stop()
		close(p.purgeDone)
	}()

	for {
		select {
		case <-ticker.C:
		case <-p.stopPurge:
			return
		}

		if p.IsClosed() {
			return
		}

		p.lock.Lock()
		expired := p.workers.refresh(p.expiryDuration)
		p.lock.Unlock()

		// Send exit signal to expired workers
		for _, w := range expired {
			w.task <- nil
		}

		// Wake up waiting submitters if all workers expired
		if p.Running() == 0 || (p.Waiting() > 0) {
			p.cond.Broadcast()
		}
	}
}

// ─── Submit ───────────────────────────────────────────────────────

// Submit sends a task to the pool for execution.
// Blocks if pool is at capacity and nonblocking is false.
// Returns ErrPoolOverload in nonblocking mode when at capacity.
// Returns ErrPoolClosed if pool is released.
func (p *Pool) Submit(task func()) error {
	if p.IsClosed() {
		return ErrPoolClosed
	}

	p.taskWg.Add(1)
	p.submitted.Add(1)

	w, err := p.retrieveWorker()
	if err != nil {
		p.taskWg.Done()
		p.submitted.Add(-1)
		return err
	}

	w.task <- task
	return nil
}

// TrySubmit submits a task without blocking. Returns false if pool is full or closed.
func (p *Pool) TrySubmit(task func()) bool {
	if p.IsClosed() {
		return false
	}

	p.taskWg.Add(1)
	p.submitted.Add(1)

	// Try to get a worker without waiting
	p.lock.Lock()
	w := p.workers.pop()
	if w != nil {
		p.lock.Unlock()
		w.task <- task
		return true
	}

	// No idle worker — try to create one if within capacity
	cap := p.Cap()
	if cap == 0 || p.Running() < cap {
		p.lock.Unlock()
		w = p.workerCache.Get().(*worker)
		w.run()
		w.task <- task
		return true
	}
	p.lock.Unlock()

	p.taskWg.Done()
	p.submitted.Add(-1)
	return false
}

// ─── Worker Retrieval ─────────────────────────────────────────────

// retrieveWorker gets an idle worker or creates a new one.
func (p *Pool) retrieveWorker() (*worker, error) {
	p.lock.Lock()

	// 1. Try to get an idle worker from the stack
	if w := p.workers.pop(); w != nil {
		p.lock.Unlock()
		return w, nil
	}

	// 2. No idle worker — can we create a new one?
	cap := int(p.capacity.Load())
	if cap == 0 || int(p.running.Load()) < cap {
		p.lock.Unlock()
		w := p.workerCache.Get().(*worker)
		w.run()
		return w, nil
	}

	// 3. At capacity — block or return error
	if p.nonblocking {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	// 4. Check max blocking limit
	if p.maxBlocking > 0 && int(p.blocking.Load()) >= p.maxBlocking {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	// 5. Wait for a worker to become available
	p.blocking.Add(1)
	defer p.blocking.Add(-1)

retry:
	if p.IsClosed() {
		p.lock.Unlock()
		return nil, ErrPoolClosed
	}

	p.cond.Wait() // releases lock, waits, re-acquires lock

	if w := p.workers.pop(); w != nil {
		p.lock.Unlock()
		return w, nil
	}

	// Spurious wakeup or closed
	if p.IsClosed() {
		p.lock.Unlock()
		return nil, ErrPoolClosed
	}

	goto retry
}

// revertWorker returns a worker to the idle stack for reuse.
func (p *Pool) revertWorker(w *worker) bool {
	if p.IsClosed() {
		return false
	}

	cap := int(p.capacity.Load())
	if cap > 0 && int(p.running.Load()) > cap {
		return false // pool was shrunk, this worker should exit
	}

	w.lastUsed.Store(time.Now().UnixNano())

	p.lock.Lock()
	p.workers.push(w)
	p.cond.Signal() // wake one waiting submitter
	p.lock.Unlock()

	return true
}

// ─── Control ──────────────────────────────────────────────────────

// Wait blocks until all submitted tasks are completed.
func (p *Pool) Wait() {
	p.taskWg.Wait()
}

// Release closes the pool. No new tasks accepted.
// All running tasks will complete. Idle workers are stopped.
func (p *Pool) Release() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}

	if p.expiryDuration > 0 {
		close(p.stopPurge)
		<-p.purgeDone
	}

	p.lock.Lock()
	p.workers.reset()
	p.lock.Unlock()

	p.cond.Broadcast()
}

// Close is an alias for Release (backwards compatibility).
func (p *Pool) Close() {
	p.Release()
}

// Reboot restarts a released pool.
func (p *Pool) Reboot() {
	if !p.closed.CompareAndSwap(true, false) {
		return
	}

	p.once = sync.Once{}
	p.allDone = make(chan struct{})
	p.stopPurge = make(chan struct{})
	p.purgeDone = make(chan struct{})

	if p.expiryDuration > 0 {
		go p.purgeStaleWorkers()
	}
}

// Tune changes the pool capacity at runtime.
func (p *Pool) Tune(size int) {
	old := p.capacity.Load()
	if size < 0 || int32(size) == old {
		return
	}
	p.capacity.Store(int32(size))

	// If capacity increased, wake up blocked submitters
	if int32(size) > old {
		diff := int(int32(size) - old)
		for i := 0; i < diff; i++ {
			p.cond.Signal()
		}
	}
}

// ─── Getters ──────────────────────────────────────────────────────

// Running returns the number of active goroutines.
func (p *Pool) Running() int {
	return int(p.running.Load())
}

// Free returns the number of available goroutines.
func (p *Pool) Free() int {
	cap := p.Cap()
	if cap == 0 {
		return -1 // unlimited
	}
	return cap - p.Running()
}

// Cap returns the pool capacity.
func (p *Pool) Cap() int {
	return int(p.capacity.Load())
}

// Waiting returns the number of goroutines blocked on Submit.
func (p *Pool) Waiting() int {
	return int(p.blocking.Load())
}

// IsClosed checks if the pool is closed.
func (p *Pool) IsClosed() bool {
	return p.closed.Load()
}

// ActiveWorkers returns the number of running workers (alias for Running).
func (p *Pool) ActiveWorkers() int {
	return p.Running()
}

// ─── Internal ─────────────────────────────────────────────────────

func (p *Pool) addRunning(delta int32) int32 {
	return p.running.Add(delta)
}

// ─── Metrics ──────────────────────────────────────────────────────

// PoolMetrics contains pool performance statistics.
type PoolMetrics struct {
	Workers    int
	Running    int
	Idle       int
	Capacity   int
	Submitted  int64
	Completed  int64
	Failed     int64
	Panicked   int64
	Waiting    int
	AvgLatency time.Duration
	MaxLatency time.Duration
}

// Metrics returns pool performance metrics.
func (p *Pool) Metrics() PoolMetrics {
	completed := p.completed.Load()
	var avgLatency time.Duration
	if completed > 0 {
		avgLatency = time.Duration(p.totalTime.Load() / completed)
	}

	running := p.Running()
	cap := p.Cap()
	idle := 0
	p.lock.Lock()
	idle = p.workers.len()
	p.lock.Unlock()

	return PoolMetrics{
		Workers:    cap,
		Running:    running,
		Idle:       idle,
		Capacity:   cap,
		Submitted:  p.submitted.Load(),
		Completed:  completed,
		Failed:     p.failed.Load(),
		Panicked:   p.panicked.Load(),
		Waiting:    p.Waiting(),
		AvgLatency: avgLatency,
		MaxLatency: time.Duration(p.maxLatency.Load()),
	}
}

// ─── Backward Compatibility ───────────────────────────────────────

// Resize is an alias for Tune.
func (p *Pool) Resize(n int) {
	p.Tune(n)
}

// SubmitWithError wraps an error-returning task.
func (p *Pool) SubmitWithError(task func() error) error {
	return p.Submit(func() {
		if err := task(); err != nil {
			p.failed.Add(1)
		}
	})
}
