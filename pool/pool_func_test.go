package pool

import (
	"sync/atomic"
	"testing"
)

func TestPoolFuncBasic(t *testing.T) {
	var counter atomic.Int64
	p, err := NewWithFunc(4, func(arg any) {
		counter.Add(arg.(int64))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := 0; i < 100; i++ {
		p.Invoke(int64(1))
	}
	p.Wait()

	if got := counter.Load(); got != 100 {
		t.Errorf("expected 100, got %d", got)
	}
}

func TestPoolFuncPanicRecovery(t *testing.T) {
	var panicCount atomic.Int32
	p, _ := NewWithFunc(2, func(arg any) {
		if arg.(int) == 42 {
			panic("boom")
		}
	}, OnPanic(func(r any) { panicCount.Add(1) }))
	defer p.Close()

	p.Invoke(1)
	p.Invoke(42) // will panic
	p.Invoke(3)
	p.Wait()

	if panicCount.Load() != 1 {
		t.Errorf("expected 1 panic, got %d", panicCount.Load())
	}
}

func TestPoolFuncNonblocking(t *testing.T) {
	started := make(chan struct{})
	blocker := make(chan struct{})
	p, _ := NewWithFunc(1, func(arg any) {
		if arg.(int) == 0 {
			close(started)
			<-blocker
		}
	}, Nonblocking())
	defer p.Close()

	p.Invoke(0) // block the only worker
	<-started

	err := p.Invoke(1) // should fail
	if err != ErrPoolOverload {
		t.Errorf("expected ErrPoolOverload, got %v", err)
	}

	close(blocker)
	p.Wait()
}

func TestPoolFuncMetrics(t *testing.T) {
	p, _ := NewWithFunc(4, func(arg any) {})
	defer p.Close()

	for i := 0; i < 50; i++ {
		p.Invoke(i)
	}
	p.Wait()

	m := p.Metrics()
	if m.Submitted != 50 {
		t.Errorf("expected 50 submitted, got %d", m.Submitted)
	}
	if m.Completed != 50 {
		t.Errorf("expected 50 completed, got %d", m.Completed)
	}
}
