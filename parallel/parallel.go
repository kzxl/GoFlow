// Package parallel provides high-level parallel processing primitives.
//
// # Usage
//
//	// Map: transform items in parallel
//	results, errs := parallel.Map(urls, fetchURL, parallel.Workers(10))
//
//	// ForEach: process items with side effects
//	parallel.ForEach(items, processItem, parallel.Workers(5))
//
//	// Filter: filter items using parallel predicate
//	evens := parallel.Filter(numbers, isEven, parallel.Workers(4))
package parallel

import (
	"context"
	"sync"
)

// Option configures parallel operations.
type Option func(*config)

type config struct {
	workers int
	ctx     context.Context
}

func defaultConfig() *config {
	return &config{
		workers: 8,
		ctx:     context.Background(),
	}
}

// Workers sets the concurrency level.
func Workers(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.workers = n
		}
	}
}

// WithContext sets the context for cancellation.
func WithContext(ctx context.Context) Option {
	return func(c *config) {
		c.ctx = ctx
	}
}

// Map applies fn to each element of input in parallel, returning results
// in the same order. If any fn returns an error, it's captured in the
// corresponding position of the errors slice.
func Map[T any, R any](input []T, fn func(T) (R, error), opts ...Option) ([]R, []error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	n := len(input)
	if n == 0 {
		return nil, nil
	}

	results := make([]R, n)
	errs := make([]error, n)
	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for i, item := range input {
		select {
		case <-cfg.ctx.Done():
			for j := i; j < n; j++ {
				errs[j] = cfg.ctx.Err()
			}
			break
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int, val T) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx], errs[idx] = fn(val)
		}(i, item)
	}

	wg.Wait()

	// Check if all errors are nil
	hasErr := false
	for _, e := range errs {
		if e != nil {
			hasErr = true
			break
		}
	}
	if !hasErr {
		errs = nil
	}

	return results, errs
}

// MapSimple is like Map but for functions that don't return errors.
func MapSimple[T any, R any](input []T, fn func(T) R, opts ...Option) []R {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	n := len(input)
	if n == 0 {
		return nil
	}

	results := make([]R, n)
	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for i, item := range input {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, val T) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = fn(val)
		}(i, item)
	}

	wg.Wait()
	return results
}

// ForEach processes each element in parallel. Blocks until all are done.
func ForEach[T any](input []T, fn func(T), opts ...Option) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for _, item := range input {
		sem <- struct{}{}
		wg.Add(1)
		go func(val T) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(val)
		}(item)
	}

	wg.Wait()
}

// ForEachWithError processes each element in parallel, collecting errors.
func ForEachWithError[T any](input []T, fn func(T) error, opts ...Option) []error {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	n := len(input)
	errs := make([]error, n)
	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for i, item := range input {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, val T) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[idx] = fn(val)
		}(i, item)
	}

	wg.Wait()

	hasErr := false
	for _, e := range errs {
		if e != nil {
			hasErr = true
			break
		}
	}
	if !hasErr {
		return nil
	}
	return errs
}

// Filter returns elements for which the predicate returns true, evaluated in parallel.
// Order is preserved.
func Filter[T any](input []T, predicate func(T) bool, opts ...Option) []T {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	n := len(input)
	if n == 0 {
		return nil
	}

	keep := make([]bool, n)
	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for i, item := range input {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, val T) {
			defer wg.Done()
			defer func() { <-sem }()
			keep[idx] = predicate(val)
		}(i, item)
	}

	wg.Wait()

	var result []T
	for i, val := range input {
		if keep[i] {
			result = append(result, val)
		}
	}
	return result
}

// Reduce combines elements in parallel using an associative combiner.
// The input is split into chunks processed by separate goroutines,
// then combined into a final result.
func Reduce[T any](input []T, identity T, combiner func(T, T) T, opts ...Option) T {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	n := len(input)
	if n == 0 {
		return identity
	}
	if n == 1 {
		return combiner(identity, input[0])
	}

	workers := cfg.workers
	if workers > n {
		workers = n
	}

	chunkSize := (n + workers - 1) / workers
	partials := make([]T, workers)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			partials[w] = identity
			continue
		}

		wg.Add(1)
		go func(idx, s, e int) {
			defer wg.Done()
			acc := identity
			for i := s; i < e; i++ {
				acc = combiner(acc, input[i])
			}
			partials[idx] = acc
		}(w, start, end)
	}

	wg.Wait()

	result := identity
	for _, partial := range partials {
		result = combiner(result, partial)
	}
	return result
}
