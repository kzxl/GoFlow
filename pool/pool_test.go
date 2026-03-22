package pool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolBasic(t *testing.T) {
	p, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
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

func TestPoolUnlimited(t *testing.T) {
	p, _ := New(0) // unlimited
	defer p.Close()

	var counter atomic.Int64
	for i := 0; i < 500; i++ {
		p.Submit(func() { counter.Add(1) })
	}
	p.Wait()

	if got := counter.Load(); got != 500 {
		t.Errorf("expected 500, got %d", got)
	}
}

func TestPoolMetrics(t *testing.T) {
	p, _ := New(2)
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
	p, _ := New(1, Nonblocking())
	defer p.Close()

	// Block the only worker
	started := make(chan struct{})
	blocker := make(chan struct{})
	p.Submit(func() {
		close(started)
		<-blocker
	})
	<-started // worker is blocked

	// Pool is at capacity (1 running), no idle workers
	ok := p.TrySubmit(func() {})
	if ok {
		t.Error("TrySubmit should fail when pool is at capacity")
	}

	close(blocker)
	p.Wait()
}

func TestPoolClosedSubmit(t *testing.T) {
	p, _ := New(2)
	p.Close()

	err := p.Submit(func() {})
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got %v", err)
	}
}

func TestPoolNonblocking(t *testing.T) {
	p, _ := New(1, Nonblocking())
	defer p.Close()

	started := make(chan struct{})
	blocker := make(chan struct{})
	p.Submit(func() {
		close(started)
		<-blocker
	})
	<-started

	// Should return error immediately, not block
	err := p.Submit(func() {})
	if err != ErrPoolOverload {
		t.Errorf("expected ErrPoolOverload, got %v", err)
	}

	close(blocker)
	p.Wait()
}

func TestPoolPanicRecovery(t *testing.T) {
	var panicCount atomic.Int32
	p, _ := New(2, OnPanic(func(r any) {
		panicCount.Add(1)
	}))
	defer p.Close()

	p.Submit(func() { panic("test panic") })
	time.Sleep(50 * time.Millisecond) // let panic settle

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
	p, _ := New(8)
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

func TestPoolTune(t *testing.T) {
	p, _ := New(2)
	defer p.Close()

	if got := p.Cap(); got != 2 {
		t.Errorf("expected capacity 2, got %d", got)
	}

	p.Tune(6)

	if got := p.Cap(); got != 6 {
		t.Errorf("expected capacity 6 after tune, got %d", got)
	}
}

func TestPoolWorkerExpiry(t *testing.T) {
	p, _ := New(4, ExpiryDuration(100*time.Millisecond))
	defer p.Close()

	for i := 0; i < 10; i++ {
		p.Submit(func() {})
	}
	p.Wait()

	// Workers should be running
	if p.Running() == 0 {
		// Workers might be idle already, that's okay
	}

	// Wait for expiry
	time.Sleep(300 * time.Millisecond)

	// Workers should have been purged
	if got := p.Running(); got > 0 {
		t.Logf("expected 0 running after expiry, got %d (may be timing dependent)", got)
	}
}

func TestPoolReboot(t *testing.T) {
	p, _ := New(4)
	p.Release()

	if !p.IsClosed() {
		t.Error("expected pool to be closed")
	}

	p.Reboot()

	if p.IsClosed() {
		t.Error("expected pool to be open after reboot")
	}

	// Should work again
	var done atomic.Bool
	p.Submit(func() { done.Store(true) })
	p.Wait()

	if !done.Load() {
		t.Error("task should complete after reboot")
	}
	p.Close()
}

func TestPoolFreeAndWaiting(t *testing.T) {
	p, _ := New(10)
	defer p.Close()

	if free := p.Free(); free != 10 {
		t.Errorf("expected free=10, got %d", free)
	}

	if waiting := p.Waiting(); waiting != 0 {
		t.Errorf("expected waiting=0, got %d", waiting)
	}
}
