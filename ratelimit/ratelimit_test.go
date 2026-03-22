package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestTryWait(t *testing.T) {
	rl := New(5, Per(time.Second))

	// Should succeed 5 times (initial tokens)
	for i := 0; i < 5; i++ {
		if !rl.TryWait() {
			t.Errorf("TryWait %d should succeed", i)
		}
	}

	// Should fail (no tokens left)
	if rl.TryWait() {
		t.Error("TryWait should fail when no tokens")
	}
}

func TestWait(t *testing.T) {
	rl := New(100, Per(time.Second)) // 100 ops/sec

	start := time.Now()
	for i := 0; i < 150; i++ {
		rl.Wait()
	}
	elapsed := time.Since(start)

	// 150 ops at 100/sec: initial 100 tokens + ~50 need refill = ~500ms
	if elapsed < 400*time.Millisecond || elapsed > 1*time.Second {
		t.Errorf("expected ~500ms, got %v", elapsed)
	}
}

func TestWaitWithContextCancel(t *testing.T) {
	rl := New(1, Per(time.Second)) // 1 op/sec
	rl.Wait()                      // consume initial token

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := rl.WaitWithContext(ctx)
	if err == nil {
		t.Error("expected context deadline exceeded")
	}
}

func TestAvailable(t *testing.T) {
	rl := New(10, Per(time.Second))

	if a := rl.Available(); a != 10 {
		t.Errorf("expected 10 available, got %d", a)
	}

	rl.TryWait()
	if a := rl.Available(); a != 9 {
		t.Errorf("expected 9 available, got %d", a)
	}
}
