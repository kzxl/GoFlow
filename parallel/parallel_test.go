package parallel

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestMapSimple(t *testing.T) {
	input := []int{1, 2, 3, 4, 5}
	result := MapSimple(input, func(n int) int { return n * n }, Workers(4))

	expected := []int{1, 4, 9, 16, 25}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("[%d] expected %d, got %d", i, expected[i], v)
		}
	}
}

func TestMapWithErrors(t *testing.T) {
	input := []string{"10", "abc", "30"}
	results, errs := Map(input, func(s string) (int, error) {
		return strconv.Atoi(s)
	}, Workers(2))

	if results[0] != 10 || results[2] != 30 {
		t.Errorf("expected [10, _, 30], got %v", results)
	}
	if errs == nil || errs[1] == nil {
		t.Error("expected error at index 1")
	}
}

func TestMapPanicRecovery(t *testing.T) {
	input := []int{1, 2, 3}
	results, errs := Map(input, func(n int) (int, error) {
		if n == 2 {
			panic("boom")
		}
		return n * 10, nil
	}, Workers(2))

	if results[0] != 10 || results[2] != 30 {
		t.Errorf("non-panicking items should succeed: got %v", results)
	}
	if errs == nil || errs[1] == nil {
		t.Error("panicking item should have error")
	}
}

func TestMapContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	input := []int{1, 2, 3, 4, 5}
	_, errs := Map(input, func(n int) (int, error) {
		time.Sleep(time.Second)
		return n, nil
	}, Workers(2), WithContext(ctx))

	if errs == nil {
		t.Error("expected errors due to cancelled context")
	}
}

func TestForEach(t *testing.T) {
	input := []int{1, 2, 3, 4, 5}
	results := make([]int, 5)

	ForEach(input, func(n int) {
		results[n-1] = n * n
	}, Workers(4))

	expected := []int{1, 4, 9, 16, 25}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("[%d] expected %d, got %d", i, expected[i], v)
		}
	}
}

func TestForEachPanicRecovery(t *testing.T) {
	// Should not crash even when a worker panics
	input := []int{1, 2, 3}
	ForEach(input, func(n int) {
		if n == 2 {
			panic("boom")
		}
	}, Workers(2))
	// If we get here, panic was recovered
}

func TestForEachWithError(t *testing.T) {
	input := []int{1, 2, 3}
	errs := ForEachWithError(input, func(n int) error {
		if n == 2 {
			return fmt.Errorf("error on %d", n)
		}
		return nil
	}, Workers(2))

	if errs == nil || errs[1] == nil {
		t.Error("expected error at index 1")
	}
}

func TestFilter(t *testing.T) {
	input := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	evens := Filter(input, func(n int) bool { return n%2 == 0 }, Workers(4))

	expected := []int{2, 4, 6, 8, 10}
	if len(evens) != len(expected) {
		t.Fatalf("expected %d evens, got %d", len(expected), len(evens))
	}
	for i, v := range evens {
		if v != expected[i] {
			t.Errorf("[%d] expected %d, got %d", i, expected[i], v)
		}
	}
}

func TestReduce(t *testing.T) {
	input := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	sum := Reduce(input, 0, func(a, b int) int { return a + b }, Workers(4))

	if sum != 55 {
		t.Errorf("expected 55, got %d", sum)
	}
}

func TestReduceEmpty(t *testing.T) {
	var input []int
	sum := Reduce(input, 42, func(a, b int) int { return a + b })
	if sum != 42 {
		t.Errorf("empty reduce should return identity, got %d", sum)
	}
}

func TestMapSimpleChunked(t *testing.T) {
	input := make([]int, 100)
	for i := range input { input[i] = i }

	result := MapSimple(input, func(n int) int { return n * 2 }, Workers(4), MinChunkSize(10))

	for i, v := range result {
		if v != i*2 {
			t.Errorf("[%d] expected %d, got %d", i, i*2, v)
		}
	}
}

func TestMapSimpleEmpty(t *testing.T) {
	var input []int
	result := MapSimple(input, func(n int) int { return n })
	if result != nil {
		t.Error("empty input should return nil")
	}
}
