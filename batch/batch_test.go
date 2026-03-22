package batch

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchBySize(t *testing.T) {
	var batchCount atomic.Int32
	var totalItems atomic.Int32

	b := New(
		Size[int](5),
		Interval[int](1*time.Hour), // disable timer
		WithHandler(func(items []int) error {
			batchCount.Add(1)
			totalItems.Add(int32(len(items)))
			return nil
		}),
	)

	for i := 0; i < 12; i++ {
		b.Add(i)
	}
	b.Close()

	if bc := batchCount.Load(); bc != 3 { // 5+5+2
		t.Errorf("expected 3 batches, got %d", bc)
	}
	if ti := totalItems.Load(); ti != 12 {
		t.Errorf("expected 12 items, got %d", ti)
	}
}

func TestBatchFlush(t *testing.T) {
	var batchCount atomic.Int32
	b := New(
		Size[int](100), // large size
		Interval[int](1*time.Hour),
		WithHandler(func(items []int) error {
			batchCount.Add(1)
			return nil
		}),
	)

	b.Add(1)
	b.Add(2)
	b.Flush() // force flush

	if bc := batchCount.Load(); bc != 1 {
		t.Errorf("expected 1 batch after flush, got %d", bc)
	}

	b.Close()
}

func TestBatchOnError(t *testing.T) {
	var errCount atomic.Int32
	b := New(
		Size[int](2),
		Interval[int](1*time.Hour),
		WithHandler(func(items []int) error {
			return errSample
		}),
		OnError(func(err error, items []int) {
			errCount.Add(1)
		}),
	)

	b.Add(1)
	b.Add(2)
	b.Close()

	if ec := errCount.Load(); ec != 1 {
		t.Errorf("expected 1 error callback, got %d", ec)
	}

	stats := b.GetStats()
	if stats.ErrorCount != 1 {
		t.Errorf("expected 1 error in stats, got %d", stats.ErrorCount)
	}
}

var errSample = errType("sample error")

type errType string

func (e errType) Error() string { return string(e) }

func TestBatchStats(t *testing.T) {
	b := New(
		Size[int](3),
		Interval[int](1*time.Hour),
		WithHandler(func(items []int) error { return nil }),
	)

	for i := 0; i < 7; i++ {
		b.Add(i)
	}
	b.Close()

	stats := b.GetStats()
	if stats.ItemCount != 7 {
		t.Errorf("expected 7 items, got %d", stats.ItemCount)
	}
	if stats.BatchCount != 3 { // 3+3+1
		t.Errorf("expected 3 batches, got %d", stats.BatchCount)
	}
}
