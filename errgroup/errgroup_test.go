package errgroup

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestGroupSuccess(t *testing.T) {
	g, _ := New(Workers(4))

	var counter atomic.Int32
	for i := 0; i < 10; i++ {
		g.Go(func() error {
			counter.Add(1)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if counter.Load() != 10 {
		t.Errorf("expected 10, got %d", counter.Load())
	}
}

func TestGroupFirstError(t *testing.T) {
	g, _ := New(Workers(2))

	g.Go(func() error { return nil })
	g.Go(func() error { return fmt.Errorf("fail") })
	g.Go(func() error { return nil })

	err := g.Wait()
	if err == nil {
		t.Error("expected error")
	}
}

func TestGroupCancellation(t *testing.T) {
	g, ctx := New(Workers(4))

	g.GoWithContext(func(ctx context.Context) error {
		return fmt.Errorf("trigger cancel")
	})

	// This goroutine should see cancelled context
	g.GoWithContext(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	err := g.Wait()
	if err == nil || err.Error() != "trigger cancel" {
		t.Errorf("expected 'trigger cancel', got %v", err)
	}
	_ = ctx
}

func TestGroupTryGo(t *testing.T) {
	g, _ := New(Workers(1))

	blocker := make(chan struct{})
	g.Go(func() error {
		<-blocker
		return nil
	})

	// Should fail because semaphore is full
	ok := g.TryGo(func() error { return nil })
	if ok {
		t.Error("TryGo should return false when semaphore full")
	}

	close(blocker)
	g.Wait()
}

func TestGroupWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	g, _ := WithContext(ctx)
	g.Go(func() error { return nil })

	// Should complete without hanging
	g.Wait()
}
