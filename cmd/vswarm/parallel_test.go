package main

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunParallelDoesAllWork(t *testing.T) {
	var n atomic.Int64
	err := runParallel(16, func(i int) error {
		n.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 16 {
		t.Fatalf("ran %d times, want 16", n.Load())
	}
}

func TestRunParallelJoinsErrors(t *testing.T) {
	want := errors.New("boom")
	err := runParallel(3, func(i int) error {
		if i == 2 {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want it to wrap %v", err, want)
	}
}

func TestRunParallelEmpty(t *testing.T) {
	if err := runParallel(0, func(i int) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRunParallelRunsConcurrently(t *testing.T) {
	const n = 8
	start := time.Now()
	if err := runParallel(n, func(i int) error {
		time.Sleep(30 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed >= time.Duration(n)*30*time.Millisecond {
		t.Fatalf("took %v; tasks did not overlap", elapsed)
	}
	fmt.Printf("parallel fan-out of %d x 30ms took %v\n", n, elapsed)
}
