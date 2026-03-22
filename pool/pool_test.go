package pool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolBasic(t *testing.T) {
	p := New(Workers(4), QueueSize(100))
	defer p.Close()

	var counter atomic.Int64
	for i := 0; i < 1000; i++ {
		p.Submit(func() { counter.Add(1) })
	}
	p.Wait()

	if got := counter.Load(); got != 1000 {
		t.Errorf("expected 1000, got %d", got)
	}
}

func TestPoolMetrics(t *testing.T) {
	p := New(Workers(2), QueueSize(10))
	defer p.Close()

	for i := 0; i < 50; i++ {
		p.Submit(func() { time.Sleep(time.Microsecond) })
	}
	p.Wait()

	m := p.Metrics()
	if m.Submitted != 50 {
		t.Errorf("submitted: expected 50, got %d", m.Submitted)
	}
	if m.Completed != 50 {
		t.Errorf("completed: expected 50, got %d", m.Completed)
	}
}

func TestPoolTrySubmit(t *testing.T) {
	p := New(Workers(1), QueueSize(2))
	defer p.Close()

	// Block the only worker — wait until it actually starts
	started := make(chan struct{})
	blocker := make(chan struct{})
	p.Submit(func() {
		close(started) // signal that worker has picked this up
		<-blocker
	})
	<-started // now the worker is definitely blocked

	// Queue has capacity 2, fill it up
	ok1 := p.TrySubmit(func() {})
	ok2 := p.TrySubmit(func() {})
	// Third should fail (queue full, worker blocked)
	ok3 := p.TrySubmit(func() {})

	if !ok1 {
		t.Error("TrySubmit 1 should succeed (queue has space)")
	}
	if !ok2 {
		t.Error("TrySubmit 2 should succeed (queue has space)")
	}
	if ok3 {
		t.Error("TrySubmit 3 should fail (queue full)")
	}

	close(blocker)
	p.Wait()
}

func TestPoolClosedSubmit(t *testing.T) {
	p := New(Workers(2))
	p.Close()

	ok := p.Submit(func() {})
	if ok {
		t.Error("Submit after Close should return false")
	}
}

func TestPoolPanicRecovery(t *testing.T) {
	var panicCount atomic.Int32
	p := New(Workers(2), OnPanic(func(r any) {
		panicCount.Add(1)
	}))
	defer p.Close()

	// Submit panicking task
	p.Submit(func() { panic("test panic") })

	// Submit normal task after
	var done atomic.Bool
	p.Submit(func() { done.Store(true) })
	p.Wait()

	if panicCount.Load() != 1 {
		t.Errorf("expected 1 panic handled, got %d", panicCount.Load())
	}
	if !done.Load() {
		t.Error("normal task after panic should complete")
	}
	m := p.Metrics()
	if m.Panicked != 1 {
		t.Errorf("expected 1 panicked in metrics, got %d", m.Panicked)
	}
}

func TestPoolConcurrentSubmit(t *testing.T) {
	p := New(Workers(8), QueueSize(1000))
	defer p.Close()

	var counter atomic.Int64
	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p.Submit(func() { counter.Add(1) })
			}
		}()
	}

	wg.Wait()
	p.Wait()

	if got := counter.Load(); got != 1000 {
		t.Errorf("expected 1000, got %d", got)
	}
}

func TestPoolResize(t *testing.T) {
	p := New(Workers(2), QueueSize(100))
	defer p.Close()

	if got := p.ActiveWorkers(); got != 2 {
		t.Errorf("expected 2 active workers, got %d", got)
	}

	p.Resize(6)
	time.Sleep(10 * time.Millisecond) // let goroutines start

	if got := p.ActiveWorkers(); got != 6 {
		t.Errorf("expected 6 active workers after resize, got %d", got)
	}
}
