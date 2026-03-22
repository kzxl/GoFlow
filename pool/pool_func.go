// Package pool — pool_func.go provides PoolWithFunc, a typed worker pool.
//
// Unlike Pool (which accepts func()), PoolWithFunc accepts a fixed handler
// and callers submit arguments. This avoids closure allocation per task,
// improving GC pressure and throughput for high-volume workloads.
//
// Inspired by ants.PoolWithFunc.
//
// # Usage
//
//	p, _ := pool.NewWithFunc(8, func(arg any) {
//	    item := arg.(MyItem)
//	    process(item)
//	})
//	defer p.Close()
//
//	p.Invoke(item)
//	p.Wait()
package pool

import (
	"errors"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// PoolFunc is a typed pool that runs a fixed function with varying arguments.
// Uses the same worker reuse/expiry/blocking design as Pool.
type PoolFunc struct {
	handler  func(any)
	capacity atomic.Int32

	running  atomic.Int32
	closed   atomic.Bool
	blocking atomic.Int32

	workers     *funcWorkerStack
	workerCache sync.Pool
	lock        sync.Locker
	cond        *sync.Cond

	expiryDuration time.Duration
	nonblocking    bool
	maxBlocking    int
	panicHandler   PanicHandler
	logger         Logger

	purgeDone chan struct{}
	stopPurge chan struct{}
	taskWg    sync.WaitGroup

	// Metrics
	submitted  atomic.Int64
	completed  atomic.Int64
	panicked   atomic.Int64
	totalTime  atomic.Int64
	maxLatency atomic.Int64
}

// funcWorker is a worker goroutine for PoolFunc.
type funcWorker struct {
	pool     *PoolFunc
	args     chan any
	lastUsed atomic.Int64
}

func (w *funcWorker) run() {
	w.pool.running.Add(1)
	go func() {
		defer func() {
			w.pool.running.Add(-1)
			w.pool.workerCache.Put(w)
			w.pool.cond.Signal()
		}()

		for arg := range w.args {
			if arg == nil {
				return // exit signal
			}

			start := time.Now()
			func() {
				defer func() {
					if r := recover(); r != nil {
						w.pool.panicked.Add(1)
						if w.pool.panicHandler != nil {
							w.pool.panicHandler(r)
						} else if w.pool.logger != nil {
							w.pool.logger.Printf("goflow/pool: funcWorker panic: %v\n%s\n", r, debug.Stack())
						}
					}
				}()
				w.pool.handler(arg)
			}()

			elapsed := time.Since(start).Nanoseconds()
			w.pool.completed.Add(1)
			w.pool.totalTime.Add(elapsed)
			for {
				cur := w.pool.maxLatency.Load()
				if elapsed <= cur || w.pool.maxLatency.CompareAndSwap(cur, elapsed) {
					break
				}
			}

			w.pool.taskWg.Done()

			// Return to pool
			w.lastUsed.Store(time.Now().UnixNano())
			w.pool.lock.Lock()
			w.pool.workers.push(w)
			w.pool.cond.Signal()
			w.pool.lock.Unlock()
		}
	}()
}

// funcWorkerStack is a LIFO stack for funcWorker.
type funcWorkerStack struct {
	items  []*funcWorker
	expiry []*funcWorker
}

func newFuncWorkerStack(size int) *funcWorkerStack {
	return &funcWorkerStack{items: make([]*funcWorker, 0, size)}
}

func (ws *funcWorkerStack) push(w *funcWorker) { ws.items = append(ws.items, w) }

func (ws *funcWorkerStack) pop() *funcWorker {
	n := len(ws.items)
	if n == 0 {
		return nil
	}
	w := ws.items[n-1]
	ws.items[n-1] = nil
	ws.items = ws.items[:n-1]
	return w
}

func (ws *funcWorkerStack) refresh(duration time.Duration) []*funcWorker {
	n := len(ws.items)
	if n == 0 {
		return nil
	}
	expiryTime := time.Now().Add(-duration).UnixNano()
	lo, hi := 0, n-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if expiryTime < ws.items[mid].lastUsed.Load() {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	ws.expiry = ws.expiry[:0]
	if hi >= 0 {
		ws.expiry = append(ws.expiry, ws.items[:hi+1]...)
		m := copy(ws.items, ws.items[hi+1:])
		for i := m; i < n; i++ {
			ws.items[i] = nil
		}
		ws.items = ws.items[:m]
	}
	return ws.expiry
}

func (ws *funcWorkerStack) reset() {
	for i, w := range ws.items {
		w.args <- nil
		ws.items[i] = nil
	}
	ws.items = ws.items[:0]
}

// NewWithFunc creates a typed pool with a fixed handler function.
func NewWithFunc(capacity int, handler func(any), opts ...Option) (*PoolFunc, error) {
	if capacity < 0 {
		return nil, ErrInvalidSize
	}
	if handler == nil {
		return nil, errors.New("handler must not be nil")
	}

	p := &PoolFunc{
		handler:        handler,
		expiryDuration: time.Second,
		purgeDone:      make(chan struct{}),
		stopPurge:      make(chan struct{}),
	}
	p.capacity.Store(int32(capacity))

	// Apply shared options
	dummy := &Pool{}
	for _, opt := range opts {
		opt(dummy)
	}
	p.nonblocking = dummy.nonblocking
	p.maxBlocking = dummy.maxBlocking
	p.panicHandler = dummy.panicHandler
	p.logger = dummy.logger
	if dummy.expiryDuration > 0 {
		p.expiryDuration = dummy.expiryDuration
	}

	p.lock = &sync.Mutex{}
	p.cond = sync.NewCond(p.lock)
	p.workers = newFuncWorkerStack(0)
	p.workerCache.New = func() any {
		return &funcWorker{pool: p, args: make(chan any, 1)}
	}

	if p.expiryDuration > 0 {
		go p.purgeStaleWorkers()
	}

	return p, nil
}

func (p *PoolFunc) purgeStaleWorkers() {
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
		if p.closed.Load() {
			return
		}
		p.lock.Lock()
		expired := p.workers.refresh(p.expiryDuration)
		p.lock.Unlock()
		for _, w := range expired {
			w.args <- nil
		}
		if p.running.Load() == 0 || p.blocking.Load() > 0 {
			p.cond.Broadcast()
		}
	}
}

// Invoke submits an argument to the pool for processing.
func (p *PoolFunc) Invoke(arg any) error {
	if p.closed.Load() {
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

	w.args <- arg
	return nil
}

func (p *PoolFunc) retrieveWorker() (*funcWorker, error) {
	p.lock.Lock()

	if w := p.workers.pop(); w != nil {
		p.lock.Unlock()
		return w, nil
	}

	cap := int(p.capacity.Load())
	if cap == 0 || int(p.running.Load()) < cap {
		p.lock.Unlock()
		w := p.workerCache.Get().(*funcWorker)
		w.run()
		return w, nil
	}

	if p.nonblocking {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	if p.maxBlocking > 0 && int(p.blocking.Load()) >= p.maxBlocking {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	p.blocking.Add(1)
	defer p.blocking.Add(-1)

	for {
		if p.closed.Load() {
			p.lock.Unlock()
			return nil, ErrPoolClosed
		}
		p.cond.Wait()
		if w := p.workers.pop(); w != nil {
			p.lock.Unlock()
			return w, nil
		}
		if p.closed.Load() {
			p.lock.Unlock()
			return nil, ErrPoolClosed
		}
	}
}

// Wait blocks until all invoked tasks are done.
func (p *PoolFunc) Wait() { p.taskWg.Wait() }

// Running returns the number of active goroutines.
func (p *PoolFunc) Running() int { return int(p.running.Load()) }

// Cap returns the pool capacity.
func (p *PoolFunc) Cap() int { return int(p.capacity.Load()) }

// IsClosed checks if the pool is closed.
func (p *PoolFunc) IsClosed() bool { return p.closed.Load() }

// Close releases the pool.
func (p *PoolFunc) Close() {
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

// Tune changes the pool capacity at runtime.
func (p *PoolFunc) Tune(size int) {
	old := p.capacity.Load()
	if size < 0 || int32(size) == old {
		return
	}
	p.capacity.Store(int32(size))
	if int32(size) > old {
		for i := 0; i < int(int32(size)-old); i++ {
			p.cond.Signal()
		}
	}
}

// Metrics returns performance statistics.
func (p *PoolFunc) Metrics() PoolMetrics {
	completed := p.completed.Load()
	var avgLatency time.Duration
	if completed > 0 {
		avgLatency = time.Duration(p.totalTime.Load() / completed)
	}
	return PoolMetrics{
		Capacity:   p.Cap(),
		Running:    p.Running(),
		Submitted:  p.submitted.Load(),
		Completed:  completed,
		Panicked:   p.panicked.Load(),
		AvgLatency: avgLatency,
		MaxLatency: time.Duration(p.maxLatency.Load()),
	}
}
