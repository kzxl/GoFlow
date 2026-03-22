// Package errgroup provides an ErrorGroup pattern for parallel execution
// with first-error cancellation.
//
// Similar to golang.org/x/sync/errgroup but with bounded concurrency
// and GoFlow-style options.
//
// # Usage
//
//	g := errgroup.New(errgroup.Workers(4))
//	for _, url := range urls {
//	    g.Go(func() error { return fetch(url) })
//	}
//	if err := g.Wait(); err != nil {
//	    log.Fatal(err)
//	}
package errgroup

import (
	"context"
	"sync"
)

// Group is a collection of goroutines working on subtasks.
// The first error cancels remaining goroutines.
type Group struct {
	cancel  context.CancelFunc
	ctx     context.Context
	wg      sync.WaitGroup
	sem     chan struct{}
	errOnce sync.Once
	err     error
}

// Option configures the Group.
type Option func(*Group)

// Workers sets the maximum number of concurrent goroutines.
// 0 = unlimited (default).
func Workers(n int) Option {
	return func(g *Group) {
		if n > 0 {
			g.sem = make(chan struct{}, n)
		}
	}
}

// New creates a new ErrorGroup with a cancellable context.
func New(opts ...Option) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &Group{ctx: ctx, cancel: cancel}
	for _, opt := range opts {
		opt(g)
	}
	return g, ctx
}

// WithContext creates a new ErrorGroup from an existing context.
func WithContext(ctx context.Context, opts ...Option) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	g := &Group{ctx: ctx, cancel: cancel}
	for _, opt := range opts {
		opt(g)
	}
	return g, ctx
}

// Go calls the given function in a new goroutine.
// The first call to return a non-nil error cancels the group;
// its error will be returned by Wait.
func (g *Group) Go(fn func() error) {
	if g.sem != nil {
		g.sem <- struct{}{}
	}

	g.wg.Add(1)
	go func() {
		defer func() {
			if g.sem != nil {
				<-g.sem
			}
			g.wg.Done()
		}()

		if err := fn(); err != nil {
			g.errOnce.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	}()
}

// GoWithContext calls fn with the group context.
// Cancelled context signals that another goroutine has errored.
func (g *Group) GoWithContext(fn func(ctx context.Context) error) {
	g.Go(func() error {
		return fn(g.ctx)
	})
}

// Wait blocks until all function calls from Go have returned,
// then returns the first non-nil error (if any).
func (g *Group) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}

// TryGo is like Go but returns false if the semaphore is full.
func (g *Group) TryGo(fn func() error) bool {
	if g.sem != nil {
		select {
		case g.sem <- struct{}{}:
		default:
			return false
		}
	}

	g.wg.Add(1)
	go func() {
		defer func() {
			if g.sem != nil {
				<-g.sem
			}
			g.wg.Done()
		}()

		if err := fn(); err != nil {
			g.errOnce.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	}()
	return true
}
