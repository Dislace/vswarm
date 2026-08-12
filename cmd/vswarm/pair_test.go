package main

import (
	"errors"
	"strings"
	"testing"
)

func TestRetryIssueSurvivesALostLockRace(t *testing.T) {
	calls := 0
	out, err := retryIssue(5, 0, func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("database is locked")
		}
		return `{"token":"t"}`, nil
	})
	if err != nil {
		t.Fatalf("a run that eventually succeeds must not fail: %v", err)
	}
	if out != `{"token":"t"}` {
		t.Fatalf("out = %q, want the successful attempt's output", out)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 -- retrying must stop at the first success", calls)
	}
}

func TestRetryIssueGivesUpAndNamesTheCause(t *testing.T) {
	calls := 0
	_, err := retryIssue(3, 0, func() (string, error) {
		calls++
		return "", errors.New("database is locked")
	})
	if err == nil {
		t.Fatal("a persistently failing issue must not be reported as paired")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("the cause must survive the wrapper, got %q", err)
	}
}

func TestRetryIssueDoesNotRetryASuccess(t *testing.T) {
	calls := 0
	if _, err := retryIssue(5, 0, func() (string, error) {
		calls++
		return "{}", nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
