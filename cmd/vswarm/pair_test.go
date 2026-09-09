package main

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestTokenFileRoundTripsTheSessionId(t *testing.T) {
	raw := renderTokenFile("you@example.com", "sess-1", "tok-1")
	id, token := parseTokenFile(raw)
	if id != "sess-1" || token != "tok-1" {
		t.Fatalf("parse = (%q, %q), want (sess-1, tok-1)", id, token)
	}
	if !strings.Contains(raw, `"you@example.com" "tok-1";`) {
		t.Errorf("angie's map entry must survive the comment, got %q", raw)
	}
}

func TestParseTokenFileReadsAFileWrittenBeforeSessionIdsWereRecorded(t *testing.T) {
	id, token := parseTokenFile("\"you@example.com\" \"tok-1\";\n")
	if token != "tok-1" {
		t.Fatalf("token = %q, want tok-1", token)
	}
	if id != "" {
		t.Errorf("id = %q, want empty so the next pair mints and takes ownership", id)
	}
}

func liveSession(id string, issued, expires time.Time) session {
	return session{SessionID: id, Subject: sessionSubject, IssuedAt: issued, ExpiresAt: expires}
}

func TestReusableKeepsASessionWithLifeLeft(t *testing.T) {
	now := time.Now()
	live := []session{liveSession("a", now.Add(-24*time.Hour), now.Add(29*24*time.Hour))}
	if !reusable(live, "a", now) {
		t.Fatal("a fresh session must be reused -- re-running up is not a rotation")
	}
}

func TestReusableRenewsNearExpiry(t *testing.T) {
	now := time.Now()
	live := []session{liveSession("a", now.Add(-29*24*time.Hour), now.Add(24*time.Hour))}
	if reusable(live, "a", now) {
		t.Fatal("a session inside the renewal window must be replaced before it expires")
	}
}

func TestReusableScalesTheRenewalWindowToAShortTTL(t *testing.T) {
	now := time.Now()
	live := []session{liveSession("a", now.Add(-time.Hour), now.Add(3*time.Hour))}
	if !reusable(live, "a", now) {
		t.Fatal("a 4h session with 3h left must be reused, not rotated on every run")
	}
}

func TestReusableRejectsASessionThatIsNoLongerListed(t *testing.T) {
	now := time.Now()
	live := []session{liveSession("b", now, now.Add(30*24*time.Hour))}
	if reusable(live, "a", now) {
		t.Fatal("a revoked or expired session must not be reused")
	}
	if reusable(live, "", now) {
		t.Fatal("no recorded id means no session to reuse")
	}
}

func TestReusableRejectsASessionVswarmDoesNotOwn(t *testing.T) {
	now := time.Now()
	s := liveSession("a", now, now.Add(30*24*time.Hour))
	s.Subject = "cli-issued-session"
	if reusable([]session{s}, "a", now) {
		t.Fatal("only vswarm's own subject may be reused")
	}
}

func TestStaleSessionsNamesEveryOtherSessionVswarmOwns(t *testing.T) {
	now := time.Now()
	mine := liveSession("keep", now, now.Add(time.Hour))
	old := liveSession("old", now, now.Add(time.Hour))
	agents := session{SessionID: "agent", Subject: "cli-issued-session"}
	got := staleSessions([]session{mine, old, agents}, "keep")
	if len(got) != 1 || got[0] != "old" {
		t.Fatalf("staleSessions = %v, want [old] -- the tenant's own session is not vswarm's to revoke", got)
	}
}

func TestParseIssuedRefusesAResponseWithoutACredential(t *testing.T) {
	if _, err := parseIssued(`{"sessionId":"a"}`); err == nil {
		t.Fatal("a response with no token must not be written into angie's map")
	}
	s, err := parseIssued(`{"sessionId":"a","token":"t"}`)
	if err != nil || s.SessionID != "a" || s.Token != "t" {
		t.Fatalf("parseIssued = (%+v, %v)", s, err)
	}
}
