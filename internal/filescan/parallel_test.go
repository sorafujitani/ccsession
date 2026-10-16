package filescan

import (
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelPreservesInputOrder(t *testing.T) {
	paths := []string{"first", "second", "third", "fourth", "fifth"}
	index := make(map[string]int, len(paths))
	for i, path := range paths {
		index[path] = i
	}

	got := Parallel(paths, func(path string) (string, bool) {
		time.Sleep(time.Duration(len(paths)-index[path]) * time.Millisecond)
		return path + "-done", true
	})

	want := []string{"first-done", "second-done", "third-done", "fourth-done", "fifth-done"}
	if !slices.Equal(got, want) {
		t.Fatalf("Parallel order = %v, want %v", got, want)
	}
}

func TestParallelEmptyInputReturnsNilWithoutWork(t *testing.T) {
	got := Parallel[int](nil, func(string) (int, bool) {
		t.Fatal("callback should not run for empty input")
		return 0, true
	})

	if got != nil {
		t.Fatalf("Parallel(nil) = %#v, want nil", got)
	}
}

func TestParallelDropsRejectedItems(t *testing.T) {
	paths := []string{"keep-a", "drop", "keep-b", "skip", "keep-c"}

	got := Parallel(paths, func(path string) (string, bool) {
		switch path {
		case "drop", "skip":
			return "", false
		default:
			return path, true
		}
	})

	want := []string{"keep-a", "keep-b", "keep-c"}
	if !slices.Equal(got, want) {
		t.Fatalf("Parallel filtered = %v, want %v", got, want)
	}
}

func TestParallelSingleItem(t *testing.T) {
	got := Parallel([]string{"only"}, func(path string) (string, bool) {
		return path + "-value", true
	})

	want := []string{"only-value"}
	if !slices.Equal(got, want) {
		t.Fatalf("Parallel single item = %v, want %v", got, want)
	}
}

func TestParallelLargeInputCompletesWithinConcurrencyLimit(t *testing.T) {
	const n = 100
	paths := make([]string, n)
	for i := range paths {
		paths[i] = strconv.Itoa(i)
	}

	var active atomic.Int64
	var maxActive atomic.Int64

	got := Parallel(paths, func(path string) (int, bool) {
		current := active.Add(1)
		recordMax(&maxActive, current)
		defer active.Add(-1)

		time.Sleep(time.Millisecond)
		value, _ := strconv.Atoi(path)
		return value, true
	})

	if len(got) != n {
		t.Fatalf("Parallel large input returned %d items, want %d", len(got), n)
	}
	for i, value := range got {
		if value != i {
			t.Fatalf("Parallel large input[%d] = %d, want %d", i, value, i)
		}
	}

	limit := min(max(runtime.NumCPU()*2, 4), len(paths))
	if int(maxActive.Load()) > limit {
		t.Fatalf("max concurrent callbacks = %d, want <= %d", maxActive.Load(), limit)
	}
}

func recordMax(maxSeen *atomic.Int64, value int64) {
	for {
		seen := maxSeen.Load()
		if value <= seen || maxSeen.CompareAndSwap(seen, value) {
			return
		}
	}
}
