package main

import (
	"errors"
	"sync"
)

// runParallel runs fn for indexes 0..n-1 concurrently and joins every error,
// so one tenant's failure never masks another's. Output ordering is the
// caller's job: results must be collected by index, not printed in-flight.
func runParallel(n int, fn func(i int) error) error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn(i)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
