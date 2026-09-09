package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/dockerx"
	"github.com/dislace/vswarm/internal/render"
)

const (
	// sessionSubject marks a session as vswarm's to reconcile. t3 stamps it on
	// issue and reports it on list, which is what lets `pair` own a set of
	// sessions without keeping a registry of its own. Anything a tenant's own
	// agents issue keeps the default subject and is never swept.
	sessionSubject = "vswarm/tenant"
	sessionLabel   = "vswarm tenant credential"

	// A credential is reused until it is closer than this to expiry, so a
	// re-run of `up` is not a rotation. A shorter token_ttl than this would
	// rotate on every run, so the floor also scales down with the session's
	// own lifetime.
	sessionRenewBefore = 7 * 24 * time.Hour

	previewHostEnvPath = render.HomeDir + "/.preview-host.env"
)

// session is the shape `t3 auth session` reports. issue returns Token as well;
// list never does.
type session struct {
	SessionID string    `json:"sessionId"`
	Subject   string    `json:"subject"`
	Token     string    `json:"token"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func cmdPair(args []string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: vswarm pair <name>")
	}
	return pair(c, args[0])
}

func pair(c *config.Config, name string) error {
	if err := pairMint(c, name); err != nil {
		return err
	}
	if err := reloadProxy(); err != nil {
		return err
	}
	fmt.Printf("paired %s (token injected, proxy reloaded)\n", name)
	return nil
}

// reloadProxy is separate from minting so `up` can mint every tenant's token
// concurrently and reload angie once; concurrent reloads race and each one
// re-reads the same include glob anyway.
func reloadProxy() error {
	_, err := dockerx.Exec(proxyContainer, "angie", "-s", "reload")
	return err
}

// pairMint reconciles a tenant down to exactly one vswarm session: it reuses
// the recorded one while it has life left, mints a replacement when it does
// not, and revokes every other session it owns. Minting unconditionally is
// what left hundreds of live, fully scoped tokens behind a month of deploys.
func pairMint(c *config.Config, name string) error {
	t, ok := c.Tenant(name)
	if !ok {
		return fmt.Errorf("no such tenant %q", name)
	}
	container := "vswarm-" + name
	if err := waitHealthy(container, 150*time.Second); err != nil {
		return err
	}

	path := tokenPath(name)
	raw, _ := os.ReadFile(path)
	id, token := parseTokenFile(string(raw))

	live, err := listSessions(container)
	if err != nil {
		return err
	}
	if !reusable(live, id, time.Now()) {
		issued, err := issueSession(container, c.TokenTTL)
		if err != nil {
			return err
		}
		id, token = issued.SessionID, issued.Token
		if err := os.WriteFile(path, []byte(renderTokenFile(t.Email, id, token)), 0o600); err != nil {
			return err
		}
	}

	if err := deliverPreviewHostEnv(container, token); err != nil {
		return err
	}
	return revokeSessions(container, staleSessions(live, id))
}

func tokenPath(name string) string {
	return filepath.Join(render.GeneratedDir, "angie", "tenants", name+".token")
}

// renderTokenFile writes the session id beside the credential angie injects,
// so the next run can tell whether the token it holds is still live without
// vswarm storing anything else. angie's map include takes the comment.
func renderTokenFile(email, id, token string) string {
	return fmt.Sprintf("# session %s\n%q %q;\n", id, email, token)
}

func parseTokenFile(raw string) (id, token string) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "# session "); ok {
			id = strings.TrimSpace(rest)
			continue
		}
		if fields := strings.Split(line, `"`); len(fields) >= 4 {
			token = fields[3]
		}
	}
	return id, token
}

// reusable answers whether the recorded session is still ours and far enough
// from expiry to keep. Renewal room scales with the session's own lifetime so
// a short token_ttl does not mean rotating on every run.
func reusable(live []session, id string, now time.Time) bool {
	if id == "" {
		return false
	}
	for _, s := range live {
		if s.SessionID != id || s.Subject != sessionSubject {
			continue
		}
		room := sessionRenewBefore
		if half := s.ExpiresAt.Sub(s.IssuedAt) / 2; half < room {
			room = half
		}
		return s.ExpiresAt.Sub(now) > room
	}
	return false
}

// staleSessions is every session vswarm owns other than the one in use. It
// never names a session issued with another subject: a tenant's agents issue
// their own, and those are not vswarm's to revoke.
func staleSessions(live []session, keepID string) []string {
	var ids []string
	for _, s := range live {
		if s.Subject == sessionSubject && s.SessionID != keepID {
			ids = append(ids, s.SessionID)
		}
	}
	return ids
}

func listSessions(container string) ([]session, error) {
	out, err := retryIssue(sessionIssueAttempts, sessionIssueBackoff, func() (string, error) {
		return dockerx.Exec(container, "t3", "auth", "session", "list",
			"--base-dir", t3BaseDir, "--json")
	})
	if err != nil {
		return nil, err
	}
	var sessions []session
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &sessions); err != nil {
		return nil, fmt.Errorf("read session list: %w", err)
	}
	return sessions, nil
}

func revokeSessions(container string, ids []string) error {
	for _, id := range ids {
		if _, err := retryIssue(sessionIssueAttempts, sessionIssueBackoff, func() (string, error) {
			return dockerx.Exec(container, "t3", "auth", "session", "revoke",
				"--base-dir", t3BaseDir, id)
		}); err != nil {
			return fmt.Errorf("revoke %s: %w", id, err)
		}
	}
	return nil
}

// t3 keeps its auth sessions in SQLite under t3BaseDir, and a tenant with a
// live agent in it holds that database. Issuing then loses the race and comes
// back "database is locked" -- roughly half the time on a busy tenant, on
// stdout rather than stderr and with an exit code alone to go on. `up` is
// meant to be safe to re-run, so a single lost race must not fail it.
func issueSession(container, ttl string) (session, error) {
	out, err := retryIssue(sessionIssueAttempts, sessionIssueBackoff, func() (string, error) {
		return dockerx.Exec(container, "t3", "auth", "session", "issue",
			"--base-dir", t3BaseDir, "--ttl", ttl, "--json",
			"--subject", sessionSubject, "--label", sessionLabel)
	})
	if err != nil {
		return session{}, err
	}
	return parseIssued(out)
}

func parseIssued(out string) (session, error) {
	var s session
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil {
		return session{}, fmt.Errorf("read issued session: %w", err)
	}
	if s.Token == "" || s.SessionID == "" {
		return session{}, fmt.Errorf("issued session has no token or id")
	}
	return s, nil
}

func retryIssue(attempts int, backoff time.Duration, run func() (string, error)) (string, error) {
	var out string
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if out, err = run(); err == nil {
			return out, nil
		}
		if attempt < attempts {
			time.Sleep(backoff)
		}
	}
	return out, fmt.Errorf("issue session after %d attempts: %w", attempts, err)
}

// deliverPreviewHostEnv hands the workspace the same credential angie injects.
// The token goes over stdin: argv is visible to every process in the
// container, and the transcript of a deploy is a log.
func deliverPreviewHostEnv(container, token string) error {
	body := fmt.Sprintf("T3_PREVIEW_HOST_TOKEN=%s\n", token)
	_, err := dockerx.ExecStdin(container, body,
		"sh", "-c", "umask 077; cat > "+previewHostEnvPath)
	return err
}

func waitHealthy(container string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, err := dockerx.Output("docker", "inspect", "-f", "{{.State.Health.Status}}", container)
		if err == nil && strings.TrimSpace(out) == "healthy" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not become healthy within %s", container, timeout)
		}
		time.Sleep(3 * time.Second)
	}
}
