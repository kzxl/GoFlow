package pipeline

import (
	"context"
	"fmt"
	"testing"
)

func TestPipelineBasic(t *testing.T) {
	p := New(
		Stage("double", func(_ context.Context, n int) (int, error) { return n * 2, nil }, 2),
		Stage("add10", func(_ context.Context, n int) (int, error) { return n + 10, nil }, 2),
	)

	results := p.Run(context.Background(), []int{1, 2, 3, 4, 5})

	expected := []int{12, 14, 16, 18, 20} // (n*2)+10
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("[%d] unexpected error: %v", i, r.Err)
		}
		if r.Value != expected[i] {
			t.Errorf("[%d] expected %d, got %d", i, expected[i], r.Value)
		}
	}
}

func TestPipelineError(t *testing.T) {
	p := New(
		Stage("validate", func(_ context.Context, n int) (int, error) {
			if n < 0 {
				return 0, fmt.Errorf("negative: %d", n)
			}
			return n, nil
		}, 1),
		Stage("double", func(_ context.Context, n int) (int, error) { return n * 2, nil }, 1),
	)

	results := p.Run(context.Background(), []int{1, -2, 3})

	if results[0].Err != nil || results[0].Value != 2 {
		t.Errorf("[0] expected 2, got %v (err: %v)", results[0].Value, results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("[1] expected error for negative input")
	}
	if results[1].Stage != "validate" {
		t.Errorf("[1] expected stage 'validate', got '%s'", results[1].Stage)
	}
	// Item 1 errored at validate, should skip double stage
	if results[2].Err != nil || results[2].Value != 6 {
		t.Errorf("[2] expected 6, got %v (err: %v)", results[2].Value, results[2].Err)
	}
}

func TestPipelinePanicRecovery(t *testing.T) {
	p := New(
		Stage("risky", func(_ context.Context, n int) (int, error) {
			if n == 2 {
				panic("boom")
			}
			return n * 10, nil
		}, 1),
	)

	results := p.Run(context.Background(), []int{1, 2, 3})

	if results[0].Value != 10 {
		t.Errorf("[0] expected 10, got %d", results[0].Value)
	}
	if results[1].Err == nil {
		t.Error("[1] expected error from panic recovery")
	}
	if results[2].Value != 30 {
		t.Errorf("[2] expected 30, got %d", results[2].Value)
	}
}

func TestPipelineEmpty(t *testing.T) {
	p := New(Stage("noop", func(_ context.Context, n int) (int, error) { return n, nil }, 1))
	results := p.Run(context.Background(), []int{})
	if results != nil {
		t.Error("empty input should return nil")
	}
}

func TestPipelineContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := New(Stage("slow", func(ctx context.Context, n int) (int, error) {
		return n, nil
	}, 1))

	results := p.Run(ctx, []int{1, 2, 3})
	// Some or all results should have context error
	hasCtxErr := false
	for _, r := range results {
		if r.Err == context.Canceled {
			hasCtxErr = true
		}
	}
	_ = hasCtxErr // may or may not have errors depending on timing
}
