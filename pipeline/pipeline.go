// Package pipeline provides a multi-stage processing pipeline.
//
// Each stage runs concurrently, connected by channels.
// Data flows: Stage1 → Stage2 → Stage3 → ... → Output
//
// # Usage
//
//	p := pipeline.New[int](
//	    pipeline.Stage("double", doubleFunc, 4),
//	    pipeline.Stage("add_10", addFunc, 2),
//	)
//	results := p.Run(ctx, inputData)
package pipeline

import (
	"context"
	"fmt"
	"sync"
)

// StageFunc processes a single item in a pipeline stage.
type StageFunc[T any] func(context.Context, T) (T, error)

// StageDef defines a pipeline stage with a name, processor, and worker count.
type StageDef[T any] struct {
	Name    string
	Fn      StageFunc[T]
	Workers int
}

// Stage creates a stage definition.
func Stage[T any](name string, fn StageFunc[T], workers int) StageDef[T] {
	if workers < 1 {
		workers = 1
	}
	return StageDef[T]{Name: name, Fn: fn, Workers: workers}
}

// Result holds the output of a pipeline item.
type Result[T any] struct {
	Value T
	Err   error
	Stage string // stage where error occurred
}

// item wraps a value with index and error tracking for internal pipeline use.
type item[T any] struct {
	idx   int
	value T
	err   error
	stage string
}

// Pipeline processes items through sequential stages with concurrent workers.
type Pipeline[T any] struct {
	stages []StageDef[T]
}

// New creates a new pipeline with the given stages.
func New[T any](stages ...StageDef[T]) *Pipeline[T] {
	return &Pipeline[T]{stages: stages}
}

// Run processes all input items through the pipeline stages.
// Returns results in order.
func (p *Pipeline[T]) Run(ctx context.Context, input []T) []Result[T] {
	if len(input) == 0 {
		return nil
	}

	// Feed input into first channel
	in := make(chan item[T], len(input))
	go func() {
		defer close(in)
		for i, v := range input {
			select {
			case <-ctx.Done():
				return
			case in <- item[T]{idx: i, value: v}:
			}
		}
	}()

	// Chain stages
	var current <-chan item[T] = in
	for _, s := range p.stages {
		current = p.runStage(ctx, s, current)
	}

	// Collect results
	results := make([]Result[T], len(input))
	for it := range current {
		results[it.idx] = Result[T]{
			Value: it.value,
			Err:   it.err,
			Stage: it.stage,
		}
	}

	return results
}

func (p *Pipeline[T]) runStage(ctx context.Context, s StageDef[T], in <-chan item[T]) <-chan item[T] {
	out := make(chan item[T], cap(in))
	var wg sync.WaitGroup

	for w := 0; w < s.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range in {
				// Skip items that already errored
				if it.err != nil {
					out <- it
					continue
				}

				select {
				case <-ctx.Done():
					it.err = ctx.Err()
					it.stage = s.Name
					out <- it
				default:
					func() {
						defer func() {
							if r := recover(); r != nil {
								it.err = fmt.Errorf("panic in pipeline stage %q: %v", s.Name, r)
								it.stage = s.Name
							}
						}()
						result, err := s.Fn(ctx, it.value)
						if err != nil {
							it.err = err
							it.stage = s.Name
						} else {
							it.value = result
						}
					}()
					out <- it
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// RunStream processes items one by one as they arrive on the input channel.
// Results are sent to the returned channel.
func (p *Pipeline[T]) RunStream(ctx context.Context, input <-chan T) <-chan Result[T] {
	// Wrap input into indexed channel
	in := make(chan item[T], 100)
	go func() {
		defer close(in)
		idx := 0
		for v := range input {
			select {
			case <-ctx.Done():
				return
			case in <- item[T]{idx: idx, value: v}:
				idx++
			}
		}
	}()

	// Chain stages
	var current <-chan item[T] = in
	for _, s := range p.stages {
		current = p.runStage(ctx, s, current)
	}

	// Convert to Result channel
	out := make(chan Result[T], 100)
	go func() {
		defer close(out)
		for it := range current {
			out <- Result[T]{
				Value: it.value,
				Err:   it.err,
				Stage: it.stage,
			}
		}
	}()

	return out
}
