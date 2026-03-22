// Package circuit provides a circuit breaker for protecting external services.
//
// States: Closed → Open → HalfOpen → Closed
//
// # Usage
//
//	cb := circuit.New("payment-api",
//	    circuit.Threshold(5),
//	    circuit.Timeout(30*time.Second),
//	)
//	result, err := circuit.Execute(cb, func() (string, error) {
//	    return callPaymentAPI()
//	})
package circuit

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// State represents the circuit breaker state.
type State int32

const (
	StateClosed   State = iota // Normal operation — requests pass through
	StateOpen                  // Tripped — requests are rejected immediately
	StateHalfOpen              // Testing — limited requests pass through
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Breaker is a circuit breaker.
type Breaker struct {
	name            string
	state           atomic.Int32
	mu              sync.Mutex
	failureCount    int
	successCount    int
	threshold       int
	timeout         time.Duration
	halfOpenMax     int
	lastStateChange time.Time
	onStateChange   func(name string, from, to State)

	// Metrics
	totalRequests atomic.Int64
	totalFailures atomic.Int64
	totalSuccess  atomic.Int64
	totalRejected atomic.Int64
}

// Option configures the circuit breaker.
type Option func(*Breaker)

// Threshold sets the number of failures before opening. Default: 5.
func Threshold(n int) Option {
	return func(b *Breaker) {
		if n > 0 {
			b.threshold = n
		}
	}
}

// Timeout sets how long the circuit stays open before half-open. Default: 30s.
func Timeout(d time.Duration) Option {
	return func(b *Breaker) { b.timeout = d }
}

// HalfOpenMax sets the max requests allowed in half-open state. Default: 1.
func HalfOpenMax(n int) Option {
	return func(b *Breaker) {
		if n > 0 {
			b.halfOpenMax = n
		}
	}
}

// OnStateChange sets a callback when state changes.
func OnStateChange(fn func(name string, from, to State)) Option {
	return func(b *Breaker) { b.onStateChange = fn }
}

// New creates a new circuit breaker.
func New(name string, opts ...Option) *Breaker {
	b := &Breaker{
		name:            name,
		threshold:       5,
		timeout:         30 * time.Second,
		halfOpenMax:     1,
		lastStateChange: time.Now(),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Execute runs the function through the circuit breaker.
func Execute[T any](b *Breaker, fn func() (T, error)) (T, error) {
	var zero T

	if err := b.allowRequest(); err != nil {
		b.totalRejected.Add(1)
		return zero, err
	}

	b.totalRequests.Add(1)
	result, err := fn()

	if err != nil {
		b.recordFailure()
		return zero, err
	}

	b.recordSuccess()
	return result, nil
}

// ExecuteSimple runs an error-only function through the circuit breaker.
func ExecuteSimple(b *Breaker, fn func() error) error {
	_, err := Execute(b, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

func (b *Breaker) allowRequest() error {
	state := State(b.state.Load())

	switch state {
	case StateClosed:
		return nil

	case StateOpen:
		b.mu.Lock()
		defer b.mu.Unlock()

		if time.Since(b.lastStateChange) >= b.timeout {
			b.setState(StateHalfOpen)
			b.successCount = 0
			return nil
		}
		return fmt.Errorf("%w: %s (retry after %v)", ErrCircuitOpen, b.name,
			b.timeout-time.Since(b.lastStateChange))

	case StateHalfOpen:
		b.mu.Lock()
		defer b.mu.Unlock()

		if b.successCount+b.failureCount >= b.halfOpenMax {
			return fmt.Errorf("%w: %s (half-open limit reached)", ErrCircuitOpen, b.name)
		}
		return nil
	}

	return nil
}

func (b *Breaker) recordSuccess() {
	b.totalSuccess.Add(1)

	b.mu.Lock()
	defer b.mu.Unlock()

	state := State(b.state.Load())

	switch state {
	case StateClosed:
		b.failureCount = 0 // reset on success

	case StateHalfOpen:
		b.successCount++
		if b.successCount >= b.halfOpenMax {
			b.failureCount = 0
			b.successCount = 0
			b.setState(StateClosed)
		}
	}
}

func (b *Breaker) recordFailure() {
	b.totalFailures.Add(1)

	b.mu.Lock()
	defer b.mu.Unlock()

	state := State(b.state.Load())

	switch state {
	case StateClosed:
		b.failureCount++
		if b.failureCount >= b.threshold {
			b.setState(StateOpen)
		}

	case StateHalfOpen:
		b.failureCount = 0
		b.successCount = 0
		b.setState(StateOpen)
	}
}

func (b *Breaker) setState(new State) {
	old := State(b.state.Load())
	if old == new {
		return
	}
	b.state.Store(int32(new))
	b.lastStateChange = time.Now()

	if b.onStateChange != nil {
		b.onStateChange(b.name, old, new)
	}
}

// State returns the current state.
func (b *Breaker) State() State {
	return State(b.state.Load())
}

// Name returns the breaker name.
func (b *Breaker) Name() string {
	return b.name
}

// Reset manually resets the circuit breaker to closed state.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failureCount = 0
	b.successCount = 0
	b.setState(StateClosed)
}

// Metrics returns breaker statistics.
type Metrics struct {
	Name      string
	State     State
	Requests  int64
	Successes int64
	Failures  int64
	Rejected  int64
}

// GetMetrics returns current breaker statistics.
func (b *Breaker) GetMetrics() Metrics {
	return Metrics{
		Name:      b.name,
		State:     b.State(),
		Requests:  b.totalRequests.Load(),
		Successes: b.totalSuccess.Load(),
		Failures:  b.totalFailures.Load(),
		Rejected:  b.totalRejected.Load(),
	}
}
