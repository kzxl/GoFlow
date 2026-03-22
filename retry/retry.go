// Package retry provides configurable retry logic with exponential backoff.
//
// # Usage
//
//	result, err := retry.Do(func() (string, error) {
//	    return callUnstableAPI()
//	}, retry.MaxAttempts(5), retry.InitialDelay(100*time.Millisecond))
package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Option configures retry behavior.
type Option func(*config)

type config struct {
	maxAttempts  int
	initialDelay time.Duration
	maxDelay     time.Duration
	multiplier   float64
	jitter       bool
	retryIf      func(error) bool
	onRetry      func(attempt int, err error, delay time.Duration)
	ctx          context.Context
}

func defaultConfig() *config {
	return &config{
		maxAttempts:  3,
		initialDelay: 100 * time.Millisecond,
		maxDelay:     30 * time.Second,
		multiplier:   2.0,
		jitter:       true,
		retryIf:      func(err error) bool { return err != nil },
		ctx:          context.Background(),
	}
}

// MaxAttempts sets the maximum number of attempts.
func MaxAttempts(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxAttempts = n
		}
	}
}

// InitialDelay sets the delay before the first retry.
func InitialDelay(d time.Duration) Option {
	return func(c *config) { c.initialDelay = d }
}

// MaxDelay sets the maximum delay between retries.
func MaxDelay(d time.Duration) Option {
	return func(c *config) { c.maxDelay = d }
}

// Multiplier sets the backoff multiplier. Default: 2.0.
func Multiplier(m float64) Option {
	return func(c *config) {
		if m > 0 {
			c.multiplier = m
		}
	}
}

// WithJitter enables/disables jitter. Default: true.
func WithJitter(enabled bool) Option {
	return func(c *config) { c.jitter = enabled }
}

// RetryIf sets a custom predicate to determine if an error should be retried.
func RetryIf(fn func(error) bool) Option {
	return func(c *config) { c.retryIf = fn }
}

// OnRetry sets a callback invoked before each retry.
func OnRetry(fn func(attempt int, err error, delay time.Duration)) Option {
	return func(c *config) { c.onRetry = fn }
}

// WithContext sets the context for cancellation.
func WithContext(ctx context.Context) Option {
	return func(c *config) { c.ctx = ctx }
}

// RetryError wraps the last error with attempt information.
type RetryError struct {
	Attempts int
	Last     error
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("retry exhausted after %d attempts: %v", e.Attempts, e.Last)
}

func (e *RetryError) Unwrap() error {
	return e.Last
}

// Do executes fn with retries on failure.
func Do[T any](fn func() (T, error), opts ...Option) (T, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	var lastErr error
	var zero T

	for attempt := 1; attempt <= cfg.maxAttempts; attempt++ {
		// Check context
		select {
		case <-cfg.ctx.Done():
			return zero, cfg.ctx.Err()
		default:
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if should retry
		if !cfg.retryIf(err) {
			return zero, err
		}

		// Last attempt — don't wait
		if attempt == cfg.maxAttempts {
			break
		}

		// Calculate delay
		delay := cfg.calculateDelay(attempt)

		// Callback
		if cfg.onRetry != nil {
			cfg.onRetry(attempt, err, delay)
		}

		// Wait or cancel
		select {
		case <-cfg.ctx.Done():
			return zero, cfg.ctx.Err()
		case <-time.After(delay):
		}
	}

	return zero, &RetryError{Attempts: cfg.maxAttempts, Last: lastErr}
}

// DoSimple executes fn (no return value) with retries.
func DoSimple(fn func() error, opts ...Option) error {
	_, err := Do(func() (struct{}, error) {
		return struct{}{}, fn()
	}, opts...)
	return err
}

func (c *config) calculateDelay(attempt int) time.Duration {
	delay := float64(c.initialDelay) * math.Pow(c.multiplier, float64(attempt-1))

	if c.jitter {
		delay = delay * (0.5 + rand.Float64()*0.5) // 50-100% of calculated delay
	}

	d := time.Duration(delay)
	if d > c.maxDelay {
		d = c.maxDelay
	}

	return d
}

// IsRetryError checks if an error is a RetryError.
func IsRetryError(err error) bool {
	var re *RetryError
	return errors.As(err, &re)
}
