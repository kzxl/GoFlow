package fanout

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestScatter(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	results := Scatter(context.Background(), items, func(_ context.Context, n int) (int, error) {
		return n * n, nil
	})

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	for i, r := range results {
		expected := items[i] * items[i]
		if r.Value != expected {
			t.Errorf("[%d] expected %d, got %d", i, expected, r.Value)
		}
	}
}

func TestScatterN(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	results := ScatterN(context.Background(), items, func(_ context.Context, n int) (int, error) {
		return n * 2, nil
	}, 3)

	for i, r := range results {
		if r.Err != nil {
			t.Errorf("[%d] unexpected error: %v", i, r.Err)
		}
		if r.Value != items[i]*2 {
			t.Errorf("[%d] expected %d, got %d", i, items[i]*2, r.Value)
		}
	}
}

func TestBroadcast(t *testing.T) {
	results := Broadcast(context.Background(), "hello",
		func(_ context.Context, s string) (string, error) { return strings.ToUpper(s), nil },
		func(_ context.Context, s string) (string, error) { return fmt.Sprintf("len=%d", len(s)), nil },
	)

	if results[0].Value != "HELLO" {
		t.Errorf("[0] expected HELLO, got %s", results[0].Value)
	}
	if results[1].Value != "len=5" {
		t.Errorf("[1] expected len=5, got %s", results[1].Value)
	}
}

func TestGather(t *testing.T) {
	ch1 := make(chan int, 2)
	ch2 := make(chan int, 2)

	ch1 <- 1
	ch1 <- 2
	close(ch1)

	ch2 <- 3
	ch2 <- 4
	close(ch2)

	merged := Gather(ch1, ch2)
	var items []int
	for v := range merged {
		items = append(items, v)
	}

	if len(items) != 4 {
		t.Errorf("expected 4 items, got %d", len(items))
	}
}

func TestFirstSuccess(t *testing.T) {
	result, err := FirstSuccess(context.Background(),
		func(_ context.Context) (string, error) { return "", fmt.Errorf("fail 1") },
		func(_ context.Context) (string, error) { return "winner", nil },
		func(_ context.Context) (string, error) { return "", fmt.Errorf("fail 3") },
	)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result != "winner" {
		t.Errorf("expected 'winner', got %q", result)
	}
}

func TestFirstSuccessAllFail(t *testing.T) {
	_, err := FirstSuccess(context.Background(),
		func(_ context.Context) (string, error) { return "", fmt.Errorf("fail 1") },
		func(_ context.Context) (string, error) { return "", fmt.Errorf("fail 2") },
	)

	if err == nil {
		t.Error("expected error when all fail")
	}
}

func TestScatterEmpty(t *testing.T) {
	results := Scatter(context.Background(), []int{}, func(_ context.Context, n int) (int, error) {
		return n, nil
	})
	if results != nil {
		t.Error("empty input should return nil")
	}
}
