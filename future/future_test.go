package future

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestGoBasic(t *testing.T) {
	f := Go(func() (int, error) { return 42, nil })
	result, err := f.Get()

	if err != nil || result != 42 {
		t.Errorf("expected 42, got %d (err: %v)", result, err)
	}
}

func TestGoError(t *testing.T) {
	f := Go(func() (int, error) { return 0, fmt.Errorf("fail") })
	_, err := f.Get()

	if err == nil || err.Error() != "fail" {
		t.Errorf("expected 'fail', got %v", err)
	}
}

func TestGoPanicRecovery(t *testing.T) {
	f := Go(func() (int, error) {
		panic("boom")
	})
	_, err := f.Get()

	if err == nil {
		t.Error("expected error from panic")
	}
}

func TestGetTimeout(t *testing.T) {
	f := Go(func() (int, error) {
		time.Sleep(1 * time.Second)
		return 1, nil
	})

	_, err := f.GetTimeout(50 * time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestDone(t *testing.T) {
	blocker := make(chan struct{})
	f := Go(func() (int, error) {
		<-blocker
		return 1, nil
	})

	if f.Done() {
		t.Error("should not be done yet")
	}

	close(blocker)
	f.Get()

	if !f.Done() {
		t.Error("should be done")
	}
}

func TestThen(t *testing.T) {
	f := Go(func() (int, error) { return 5, nil })
	f2 := Then(f, func(n int) (string, error) {
		return fmt.Sprintf("result=%d", n*2), nil
	})

	result, err := f2.Get()
	if err != nil || result != "result=10" {
		t.Errorf("expected 'result=10', got %q (err: %v)", result, err)
	}
}

func TestAll(t *testing.T) {
	f1 := Go(func() (int, error) { return 1, nil })
	f2 := Go(func() (int, error) { return 2, nil })
	f3 := Go(func() (int, error) { return 3, nil })

	results, err := All(f1, f2, f3)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 3 || results[0] != 1 || results[1] != 2 || results[2] != 3 {
		t.Errorf("expected [1,2,3], got %v", results)
	}
}

func TestAllError(t *testing.T) {
	f1 := Go(func() (int, error) { return 1, nil })
	f2 := Go(func() (int, error) { return 0, fmt.Errorf("fail") })

	_, err := All(f1, f2)
	if err == nil {
		t.Error("expected error from All")
	}
}

func TestRace(t *testing.T) {
	f1 := Go(func() (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "slow", nil
	})
	f2 := Go(func() (string, error) {
		return "fast", nil
	})

	result, err := Race(f1, f2)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != "fast" {
		t.Errorf("expected 'fast', got %q", result)
	}
}

func TestGoWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f := GoWithContext(ctx, func(ctx context.Context) (int, error) {
		return 0, ctx.Err()
	})

	_, err := f.Get()
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
