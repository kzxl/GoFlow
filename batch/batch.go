// Package batch provides a batch collector that groups items and processes them together.
//
// # Usage
//
//	b := batch.New[Order](batch.Size(100), batch.Interval(5*time.Second),
//	    batch.Handler(func(orders []Order) error {
//	        return db.BulkInsert(orders)
//	    }),
//	)
//	b.Add(order)  // collected into batch
//	b.Flush()     // force process current batch
//	b.Close()     // flush remaining + stop
package batch

import (
	"sync"
	"time"
)

// Handler processes a batch of items.
type Handler[T any] func(items []T) error

// Option configures the batch processor.
type Option[T any] func(*Processor[T])

// Size sets the maximum batch size before automatic flush.
func Size[T any](n int) Option[T] {
	return func(p *Processor[T]) {
		if n > 0 {
			p.maxSize = n
		}
	}
}

// Interval sets the maximum time between flushes.
func Interval[T any](d time.Duration) Option[T] {
	return func(p *Processor[T]) {
		if d > 0 {
			p.interval = d
		}
	}
}

// WithHandler sets the batch processing function.
func WithHandler[T any](fn Handler[T]) Option[T] {
	return func(p *Processor[T]) {
		p.handler = fn
	}
}

// OnError sets the error callback.
func OnError[T any](fn func(error, []T)) Option[T] {
	return func(p *Processor[T]) {
		p.onError = fn
	}
}

// Processor collects items and processes them in batches.
type Processor[T any] struct {
	mu       sync.Mutex
	items    []T
	maxSize  int
	interval time.Duration
	handler  Handler[T]
	onError  func(error, []T)
	timer    *time.Timer
	closed   bool

	// Metrics
	batchCount    int64
	itemCount     int64
	errorCount    int64
}

// New creates a new batch processor.
func New[T any](opts ...Option[T]) *Processor[T] {
	p := &Processor[T]{
		maxSize:  100,
		interval: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(p)
	}

	if p.interval > 0 {
		p.timer = time.AfterFunc(p.interval, func() {
			p.Flush()
		})
	}

	return p
}

// Add adds an item to the current batch.
// If the batch reaches maxSize, it's automatically flushed.
func (p *Processor[T]) Add(item T) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}

	p.items = append(p.items, item)
	p.itemCount++

	if len(p.items) >= p.maxSize {
		items := p.items
		p.items = make([]T, 0, p.maxSize)
		p.resetTimer()
		p.mu.Unlock()
		p.process(items)
		return
	}
	p.mu.Unlock()
}

// AddAll adds multiple items at once.
func (p *Processor[T]) AddAll(items []T) {
	for _, item := range items {
		p.Add(item)
	}
}

// Flush forces processing of the current batch.
func (p *Processor[T]) Flush() {
	p.mu.Lock()
	if len(p.items) == 0 {
		p.resetTimer()
		p.mu.Unlock()
		return
	}

	items := p.items
	p.items = make([]T, 0, p.maxSize)
	p.resetTimer()
	p.mu.Unlock()

	p.process(items)
}

// Close flushes remaining items and stops the processor.
func (p *Processor[T]) Close() {
	p.mu.Lock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
	items := p.items
	p.items = nil
	p.mu.Unlock()

	if len(items) > 0 {
		p.process(items)
	}
}

func (p *Processor[T]) process(items []T) {
	if p.handler == nil || len(items) == 0 {
		return
	}

	p.mu.Lock()
	p.batchCount++
	p.mu.Unlock()

	if err := p.handler(items); err != nil {
		p.mu.Lock()
		p.errorCount++
		p.mu.Unlock()
		if p.onError != nil {
			p.onError(err, items)
		}
	}
}

func (p *Processor[T]) resetTimer() {
	if p.timer != nil {
		p.timer.Reset(p.interval)
	}
}

// Stats returns batch processor statistics.
type Stats struct {
	BatchCount int64
	ItemCount  int64
	ErrorCount int64
	Pending    int
}

// GetStats returns current processor statistics.
func (p *Processor[T]) GetStats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		BatchCount: p.batchCount,
		ItemCount:  p.itemCount,
		ErrorCount: p.errorCount,
		Pending:    len(p.items),
	}
}
