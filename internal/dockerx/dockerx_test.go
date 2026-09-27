package dockerx

import (
	"errors"
	"testing"
	"time"
)

func TestACallThatOutlivesItsDeadlineIsAbandonedAndSaysSo(t *testing.T) {
	start := time.Now()
	_, err := OutputWithin(100*time.Millisecond, "sleep", "30")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("OutputWithin() error = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("OutputWithin() returned after %s; a wedged call must not hold up its caller", elapsed)
	}
}
