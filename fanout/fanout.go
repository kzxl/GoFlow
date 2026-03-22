// Package fanout provides fan-out/fan-in concurrency patterns.
//
// Fan-out: one input → multiple concurrent processors
// Fan-in: multiple concurrent outputs → one merged stream
//
// # Usage
//
//	results := fanout.Scatter(data, []fanout.Worker{worker1, worker2, worker3})
//	merged := fanout.Gather(ch1, ch2, ch3)
package fanout

import (
	"context"
	"sync"
)

// ScatterFunc processes a single item.
type ScatterFunc[T any, R any] func(context.Context, T) (R, error)

// ScatterResult holds the result of a scattered operation.
type ScatterResult[R any] struct {
	WorkerID int
	Value    R
	Err      error
}

// Scatter sends each item to a dedicated worker goroutine.
// Each worker processes one item concurrently. Results are returned in worker order.
func Scatter[T any, R any](ctx context.Context, items []T, fn ScatterFunc[T, R]) []ScatterResult[R] {
	n := len(items)
	if n == 0 {
		return nil
	}

	results := make([]ScatterResult[R], n)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		go func(idx int, val T) {
			defer wg.Done()
			value, err := fn(ctx, val)
			results[idx] = ScatterResult[R]{
				WorkerID: idx,
				Value:    value,
				Err:      err,
			}
		}(i, item)
	}

	wg.Wait()
	return results
}

// ScatterN sends all items through N workers (bounded fan-out).
func ScatterN[T any, R any](ctx context.Context, items []T, fn ScatterFunc[T, R], workers int) []ScatterResult[R] {
	n := len(items)
	if n == 0 {
		return nil
	}
	if workers <= 0 {
		workers = 1
	}

	results := make([]ScatterResult[R], n)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for i, item := range items {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, val T) {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				results[idx] = ScatterResult[R]{WorkerID: idx, Err: ctx.Err()}
				return
			default:
			}

			value, err := fn(ctx, val)
			results[idx] = ScatterResult[R]{
				WorkerID: idx,
				Value:    value,
				Err:      err,
			}
		}(i, item)
	}

	wg.Wait()
	return results
}

// Broadcast sends the SAME input to multiple functions concurrently.
// Each function gets its own copy of the input.
func Broadcast[T any, R any](ctx context.Context, input T, fns ...ScatterFunc[T, R]) []ScatterResult[R] {
	n := len(fns)
	if n == 0 {
		return nil
	}

	results := make([]ScatterResult[R], n)
	var wg sync.WaitGroup

	for i, fn := range fns {
		wg.Add(1)
		go func(idx int, f ScatterFunc[T, R]) {
			defer wg.Done()
			value, err := f(ctx, input)
			results[idx] = ScatterResult[R]{
				WorkerID: idx,
				Value:    value,
				Err:      err,
			}
		}(i, fn)
	}

	wg.Wait()
	return results
}

// Gather merges multiple channels into one.
func Gather[T any](channels ...<-chan T) <-chan T {
	out := make(chan T, len(channels))
	var wg sync.WaitGroup

	for _, ch := range channels {
		wg.Add(1)
		go func(c <-chan T) {
			defer wg.Done()
			for val := range c {
				out <- val
			}
		}(ch)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// FirstSuccess runs all functions concurrently and returns the first successful result.
// Remaining goroutines are cancelled via context.
func FirstSuccess[T any](ctx context.Context, fns ...func(context.Context) (T, error)) (T, error) {
	var zero T
	if len(fns) == 0 {
		return zero, context.Canceled
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		val T
		err error
	}

	ch := make(chan result, len(fns))

	for _, fn := range fns {
		go func(f func(context.Context) (T, error)) {
			val, err := f(ctx)
			select {
			case ch <- result{val, err}:
			case <-ctx.Done():
			}
		}(fn)
	}

	var lastErr error
	for range fns {
		select {
		case r := <-ch:
			if r.err == nil {
				return r.val, nil
			}
			lastErr = r.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	return zero, lastErr
}
