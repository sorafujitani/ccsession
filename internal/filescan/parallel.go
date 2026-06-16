package filescan

import (
	"runtime"
	"sync"
)

func Parallel[T any](paths []string, fn func(string) (T, bool)) []T {
	if len(paths) == 0 {
		return nil
	}
	values := make([]T, len(paths))
	ok := make([]bool, len(paths))
	concurrency := min(max(runtime.NumCPU()*2, 4), len(paths))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			value, keep := fn(path)
			if keep {
				values[i] = value
				ok[i] = true
			}
		}()
	}
	wg.Wait()

	out := make([]T, 0, len(paths))
	for i, keep := range ok {
		if keep {
			out = append(out, values[i])
		}
	}
	return out
}
