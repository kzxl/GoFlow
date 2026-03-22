package retry

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDoSuccess(t *testing.T) {
	result, err := Do(func() (string, error) {
		return "ok", nil
	})

	if err != nil || result != "ok" {
		t.Errorf("expected 'ok', got %q (err: %v)", result, err)
	}
}

func TestDoRetryThenSuccess(t *testing.T) {
	attempt := 0
	result, err := Do(func() (int, error) {
		attempt++
		if attempt < 3 {
			return 0, fmt.Errorf("fail %d", attempt)
		}
		return 42, nil
	}, MaxAttempts(5), InitialDelay(time.Millisecond), WithJitter(false))

	if err != nil || result != 42 {
		t.Errorf("expected 42, got %d (err: %v)", result, err)
	}
	if attempt != 3 {
		t.Errorf("expected 3 attempts, got %d", attempt)
	}
}

func TestDoExhausted(t *testing.T) {
	_, err := Do(func() (int, error) {
		return 0, fmt.Errorf("always fail")
	}, MaxAttempts(3), InitialDelay(time.Millisecond), WithJitter(false))

	if err == nil {
		t.Error("expected error after exhausted retries")
	}
	if !IsRetryError(err) {
		t.Errorf("expected RetryError, got %T", err)
	}
}

func TestDoRetryIf(t *testing.T) {
	attempt := 0
	_, err := Do(func() (int, error) {
		attempt++
		return 0, fmt.Errorf("permanent error")
	}, MaxAttempts(5), RetryIf(func(err error) bool {
		return false // never retry
	}))

	if attempt != 1 {
		t.Errorf("should only try once with RetryIf returning false, got %d", attempt)
	}
	if err == nil {
		t.Error("expected error")
	}
}

func TestDoOnRetry(t *testing.T) {
	var retried int
	Do(func() (int, error) {
		return 0, fmt.Errorf("fail")
	}, MaxAttempts(3), InitialDelay(time.Millisecond), WithJitter(false),
		OnRetry(func(a int, err error, d time.Duration) {
			retried++
		}),
	)

	if retried != 2 { // 3 attempts = 2 retries
		t.Errorf("expected 2 OnRetry calls, got %d", retried)
	}
}

func TestDoContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Do(func() (int, error) {
		return 0, fmt.Errorf("fail")
	}, MaxAttempts(10), WithContext(ctx))

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestDoSimple(t *testing.T) {
	attempt := 0
	err := DoSimple(func() error {
		attempt++
		if attempt < 2 {
			return fmt.Errorf("fail")
		}
		return nil
	}, MaxAttempts(3), InitialDelay(time.Millisecond), WithJitter(false))

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
