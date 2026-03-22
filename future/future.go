// Package future provides a Future/Promise pattern for async results.
//
// Submit a function and get a Future back immediately. The function runs
// asynchronously. Call Future.Get() to wait for the result.
//
// # Usage
//
//	f := future.Go(func() (int, error) { return expensiveCalc(), nil })
//	// ... do other work ...
//	result, err := f.Get()          // blocks until done
//	result, err := f.GetTimeout(5s) // with timeout
package future

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Future represents an async computation result.
type Future[T any] struct {
	value T
	err   error
	done  chan struct{}
	once  sync.Once
}

// Go launches an async computation and returns a Future.
func Go[T any](fn func() (T, error)) *Future[T] {
	f := &Future[T]{done: make(chan struct{})}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				f.err = fmt.Errorf("future panic: %v", r)
			}
			close(f.done)
		}()
		f.value, f.err = fn()
	}()
	return f
}

// GoWithContext launches fn with context for cancellation.
func GoWithContext[T any](ctx context.Context, fn func(ctx context.Context) (T, error)) *Future[T] {
	f := &Future[T]{done: make(chan struct{})}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				f.err = fmt.Errorf("future panic: %v", r)
			}
			close(f.done)
		}()
		f.value, f.err = fn(ctx)
	}()
	return f
}

// Get blocks until the Future is resolved and returns the result.
func (f *Future[T]) Get() (T, error) {
	<-f.done
	return f.value, f.err
}

// GetTimeout blocks until result or timeout.
func (f *Future[T]) GetTimeout(d time.Duration) (T, error) {
	select {
	case <-f.done:
		return f.value, f.err
	case <-time.After(d):
		var zero T
		return zero, context.DeadlineExceeded
	}
}

// GetContext blocks until result or context cancellation.
func (f *Future[T]) GetContext(ctx context.Context) (T, error) {
	select {
	case <-f.done:
		return f.value, f.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Done returns true if the future has completed.
func (f *Future[T]) Done() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// Then chains a transformation on the result.
func Then[T any, R any](f *Future[T], fn func(T) (R, error)) *Future[R] {
	return Go(func() (R, error) {
		val, err := f.Get()
		if err != nil {
			var zero R
			return zero, err
		}
		return fn(val)
	})
}

// All waits for all futures to complete and returns all results.
func All[T any](futures ...*Future[T]) ([]T, error) {
	results := make([]T, len(futures))
	for i, f := range futures {
		val, err := f.Get()
		if err != nil {
			return nil, err
		}
		results[i] = val
	}
	return results, nil
}

// Race returns the result of the first future to complete.
func Race[T any](futures ...*Future[T]) (T, error) {
	done := make(chan int, len(futures))
	for i, f := range futures {
		idx := i
		go func() {
			<-f.done
			done <- idx
		}()
	}
	winner := <-done
	return futures[winner].Get()
}
