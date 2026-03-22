package circuit

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCircuitClosed(t *testing.T) {
	cb := New("test", Threshold(3))

	result, err := Execute(cb, func() (string, error) {
		return "ok", nil
	})

	if err != nil || result != "ok" {
		t.Errorf("expected 'ok', got %q (err: %v)", result, err)
	}
	if cb.State() != StateClosed {
		t.Errorf("expected CLOSED, got %s", cb.State())
	}
}

func TestCircuitOpensAfterThreshold(t *testing.T) {
	cb := New("test", Threshold(3), Timeout(1*time.Hour))

	// 3 failures should open
	for i := 0; i < 3; i++ {
		Execute(cb, func() (int, error) { return 0, fmt.Errorf("fail") })
	}

	if cb.State() != StateOpen {
		t.Errorf("expected OPEN after 3 failures, got %s", cb.State())
	}

	// Next call should be rejected
	_, err := Execute(cb, func() (int, error) { return 1, nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitHalfOpenRecovery(t *testing.T) {
	cb := New("test", Threshold(2), Timeout(50*time.Millisecond))

	// Open it
	Execute(cb, func() (int, error) { return 0, fmt.Errorf("f1") })
	Execute(cb, func() (int, error) { return 0, fmt.Errorf("f2") })

	if cb.State() != StateOpen {
		t.Fatalf("expected OPEN, got %s", cb.State())
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Should transition to half-open and allow one request
	result, err := Execute(cb, func() (string, error) { return "recovered", nil })
	if err != nil {
		t.Errorf("expected success in half-open, got %v", err)
	}
	if result != "recovered" {
		t.Errorf("expected 'recovered', got %q", result)
	}

	// Should be closed again
	if cb.State() != StateClosed {
		t.Errorf("expected CLOSED after recovery, got %s", cb.State())
	}
}

func TestCircuitStateChangeCallback(t *testing.T) {
	var transitions []string
	cb := New("test", Threshold(2), Timeout(50*time.Millisecond),
		OnStateChange(func(name string, from, to State) {
			transitions = append(transitions, fmt.Sprintf("%s→%s", from, to))
		}),
	)

	Execute(cb, func() (int, error) { return 0, fmt.Errorf("f1") })
	Execute(cb, func() (int, error) { return 0, fmt.Errorf("f2") })

	if len(transitions) != 1 || transitions[0] != "CLOSED→OPEN" {
		t.Errorf("expected [CLOSED→OPEN], got %v", transitions)
	}
}

func TestCircuitReset(t *testing.T) {
	cb := New("test", Threshold(1))

	Execute(cb, func() (int, error) { return 0, fmt.Errorf("fail") })
	if cb.State() != StateOpen {
		t.Fatalf("expected OPEN, got %s", cb.State())
	}

	cb.Reset()
	if cb.State() != StateClosed {
		t.Errorf("expected CLOSED after reset, got %s", cb.State())
	}
}

func TestCircuitMetrics(t *testing.T) {
	cb := New("test", Threshold(10))

	Execute(cb, func() (int, error) { return 1, nil })
	Execute(cb, func() (int, error) { return 0, fmt.Errorf("fail") })
	Execute(cb, func() (int, error) { return 1, nil })

	m := cb.GetMetrics()
	if m.Requests != 3 {
		t.Errorf("expected 3 requests, got %d", m.Requests)
	}
	if m.Successes != 2 {
		t.Errorf("expected 2 successes, got %d", m.Successes)
	}
	if m.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", m.Failures)
	}
}

func TestCircuitSuccessResetsFailures(t *testing.T) {
	cb := New("test", Threshold(3))

	Execute(cb, func() (int, error) { return 0, fmt.Errorf("f1") })
	Execute(cb, func() (int, error) { return 0, fmt.Errorf("f2") })
	Execute(cb, func() (int, error) { return 1, nil }) // reset

	// Should not be open
	if cb.State() != StateClosed {
		t.Errorf("expected CLOSED after success reset, got %s", cb.State())
	}
}
