// Package ratelimit provides a token bucket rate limiter.
//
// # Usage
//
//	rl := ratelimit.New(100, ratelimit.Per(time.Second))  // 100 ops/sec
//	rl.Wait()      // blocks until a token is available
//	rl.TryWait()   // non-blocking, returns false if no token
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter is a token bucket rate limiter.
type Limiter struct {
	mu       sync.Mutex
	tokens   float64
	maxTokens float64
	refillRate float64 // tokens per nanosecond
	lastRefill time.Time
}

// Option configures the rate limiter.
type Option func(*config)

type config struct {
	per time.Duration
}

// Per sets the time window for the rate. Default: 1 second.
func Per(d time.Duration) Option {
	return func(c *config) { c.per = d }
}

// New creates a rate limiter that allows `rate` operations per time window.
func New(rate int, opts ...Option) *Limiter {
	cfg := &config{per: time.Second}
	for _, opt := range opts {
		opt(cfg)
	}

	return &Limiter{
		tokens:    float64(rate),
		maxTokens: float64(rate),
		refillRate: float64(rate) / float64(cfg.per.Nanoseconds()),
		lastRefill: time.Now(),
	}
}

// refill adds tokens based on elapsed time.
func (l *Limiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Nanoseconds()
	l.tokens += float64(elapsed) * l.refillRate
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
	l.lastRefill = now
}

// Wait blocks until a token is available or context is cancelled.
func (l *Limiter) Wait() {
	l.WaitN(1)
}

// WaitN blocks until n tokens are available.
func (l *Limiter) WaitN(n int) {
	for {
		l.mu.Lock()
		l.refill()
		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return
		}
		// Calculate wait time
		deficit := float64(n) - l.tokens
		waitNs := deficit / l.refillRate
		l.mu.Unlock()

		time.Sleep(time.Duration(waitNs))
	}
}

// WaitWithContext blocks until a token is available or context is cancelled.
func (l *Limiter) WaitWithContext(ctx context.Context) error {
	return l.WaitNWithContext(ctx, 1)
}

// WaitNWithContext blocks until n tokens are available or context is cancelled.
func (l *Limiter) WaitNWithContext(ctx context.Context, n int) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		l.mu.Lock()
		l.refill()
		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return nil
		}
		deficit := float64(n) - l.tokens
		waitNs := deficit / l.refillRate
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(waitNs)):
		}
	}
}

// TryWait acquires a token without blocking.
// Returns true if a token was acquired, false otherwise.
func (l *Limiter) TryWait() bool {
	return l.TryWaitN(1)
}

// TryWaitN tries to acquire n tokens without blocking.
func (l *Limiter) TryWaitN(n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()
	if l.tokens >= float64(n) {
		l.tokens -= float64(n)
		return true
	}
	return false
}

// Available returns the current number of available tokens.
func (l *Limiter) Available() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()
	return int(l.tokens)
}
