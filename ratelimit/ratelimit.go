// Package ratelimit provides a token bucket rate limiter.
//
// Uses time.Ticker for accurate rate limiting instead of spin loops.
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
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per nanosecond
	lastRefill time.Time
	interval   time.Duration // time between single token refills
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

	interval := cfg.per / time.Duration(rate)

	return &Limiter{
		tokens:     float64(rate),
		maxTokens:  float64(rate),
		refillRate: float64(rate) / float64(cfg.per.Nanoseconds()),
		lastRefill: time.Now(),
		interval:   interval,
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

// Wait blocks until a token is available.
func (l *Limiter) Wait() {
	l.WaitN(1)
}

// WaitN blocks until n tokens are available.
// Uses precise sleep-based waiting instead of spin loops.
func (l *Limiter) WaitN(n int) {
	for {
		l.mu.Lock()
		l.refill()
		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return
		}
		// Calculate precise wait time for needed tokens
		deficit := float64(n) - l.tokens
		waitNs := deficit / l.refillRate
		l.mu.Unlock()

		// Use a timer for precise sleep (better than time.Sleep for accuracy)
		timer := time.NewTimer(time.Duration(waitNs))
		<-timer.C
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

		timer := time.NewTimer(time.Duration(waitNs))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
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
