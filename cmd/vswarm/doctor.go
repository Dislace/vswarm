package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/dockerx"
	"github.com/dislace/vswarm/internal/render"
)

type checkResult struct {
	name   string
	pass   bool
	detail string
}

// tenantChecks fans one check out across every tenant concurrently and
// returns results in config order. A zero checkResult means "not applicable".
func tenantChecks(c *config.Config, fn func(t config.Tenant) checkResult) []checkResult {
	rs := make([]checkResult, len(c.Tenants))
	_ = runParallel(len(c.Tenants), func(i int) error {
		rs[i] = fn(c.Tenants[i])
		return nil
	})
	return rs
}

// cmdDoctor retries the whole check set until it passes or --wait runs out.
// The checks watch a stack that is still settling — a workspace becomes
// healthy, a token starts authenticating — so callers were wrapping doctor in
// their own retry loop and could only re-run the slow parts blind.
func cmdDoctor(args []string) error {
	rest, wait, err := takeDuration(args, "--wait")
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("usage: vswarm doctor [--wait=<duration>]")
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}

	deadline := time.Now().Add(wait)
	for {
		results := doctorChecks(c)
		last := time.Now().After(deadline)
		if doctorPassed(results) || last {
			return reportDoctor(results)
		}
		time.Sleep(doctorRetryBackoff)
	}
}

const doctorRetryBackoff = 3 * time.Second

// takeDuration pulls --name=<duration> out of an argument list. A zero
// duration is the default and means one pass, which is what doctor did before.
func takeDuration(args []string, name string) ([]string, time.Duration, error) {
	var rest []string
	var d time.Duration
	for _, a := range args {
		raw, ok := strings.CutPrefix(a, name+"=")
		if !ok {
			rest = append(rest, a)
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("%s=%q: %w", name, raw, err)
		}
		d = parsed
	}
	return rest, d, nil
}

func doctorPassed(results []checkResult) bool {
	for _, r := range results {
		if r.name != "" && !r.pass {
			return false
		}
	}
	return true
}

func reportDoctor(results []checkResult) error {
	ok := true
	for _, r := range results {
		if r.name == "" {
			continue
		}
		mark := "PASS"
		if !r.pass {
			mark, ok = "FAIL", false
		}
		fmt.Printf("[%s] %s%s\n", mark, r.name, detailSuffix(r.detail))
	}
	if !ok {
		return fmt.Errorf("doctor: one or more checks FAILED")
	}
	fmt.Println("doctor: all checks passed")
	return nil
}

func doctorChecks(c *config.Config) []checkResult {
	var results []checkResult
	check := func(name string, pass bool, detail string) {
		results = append(results, checkResult{name, pass, detail})
	}

	_, statErr := os.Stat("generated/docker-compose.yml")
	check("rendered compose present", statErr == nil, "")

	_, aerr := dockerx.Exec(proxyContainer, "angie", "-t")
	check("angie -t config valid", aerr == nil, errStr(aerr))

	answered, detail := proxyAnswersPreflight()
	check("proxy answers CORS preflight without identity", answered, detail)

	results = append(results, edgeForwardsPreflight(c.Domain))

	published, detail := anyPublishedPorts()
	check("no published host ports", !published, detail)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			reach := tenantReachesProxy("vswarm-" + t.Name)
			return checkResult{name: "isolation: " + t.Name + " cannot reach proxy", pass: !reach}
		})...)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			authed, detail := tenantTokenAuthenticates(t.Name)
			return checkResult{"token authenticates for " + t.Name, authed, detail}
		})...)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			ok, detail := oneLiveSession(t.Name, time.Now())
			return checkResult{"exactly one vswarm session for " + t.Name, ok, detail}
		})...)

	volumeResults := make([][]checkResult, len(c.Tenants))
	_ = runParallel(len(c.Tenants), func(i int) error {
		t := c.Tenants[i]
		var rs []checkResult
		for _, vol := range []string{render.WorkVolume(t.Name), render.CacheVolume(t.Name)} {
			_, verr := dockerx.Output("docker", "volume", "inspect", vol)
			rs = append(rs, checkResult{"volume present: " + vol, verr == nil, errStr(verr)})
		}
		volumeResults[i] = rs
		return nil
	})
	for _, rs := range volumeResults {
		results = append(results, rs...)
	}

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			mounted, detail := cacheMountTook("vswarm-" + t.Name)
			return checkResult{"cache is a separate volume for " + t.Name, mounted, detail}
		})...)

	for _, m := range c.Mounts {
		mount := m
		results = append(results,
			tenantChecks(c, func(t config.Tenant) checkResult {
				took, detail := declaredMountTook("vswarm-"+t.Name, mount.Target)
				return checkResult{"read-only mount at " + mount.Target + " for " + t.Name, took, detail}
			})...)
	}

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			mode, merr := containerMode("vswarm-"+t.Name, render.HomeDir+"/.ssh")
			return checkResult{"ssh perms 700 for " + t.Name, merr == nil && mode == "700", pathDetail(mode, merr)}
		})...)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			if t.Admin {
				return checkResult{}
			}
			has, detail := containerHas("vswarm-"+t.Name, adminKeyPath())
			return checkResult{"no admin key in non-admin home: " + t.Name,
				has.provesAbsence(), detail}
		})...)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			if !t.Admin {
				return checkResult{}
			}
			mode, merr := containerMode("vswarm-"+t.Name, adminKeyPath())
			return checkResult{"admin key 0600 for " + t.Name, merr == nil && mode == "600", pathDetail(mode, merr)}
		})...)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			if !t.HasService("postgres") {
				return checkResult{}
			}
			nets, nerr := dbNetworks("vswarm-db-" + t.Name)
			want := "vswarm-net-" + t.Name
			onlyOwn := nerr == nil && len(nets) == 1 && nets[0] == want
			return checkResult{"db " + t.Name + " on exactly its network", onlyOwn, dbNetDetail(nets, nerr)}
		})...)

	results = append(results,
		tenantChecks(c, func(t config.Tenant) checkResult {
			if !t.HasService("playwright") {
				return checkResult{}
			}
			_, err := dockerx.Exec("vswarm-"+t.Name, "curl", "-sS", "-m", "5",
				"-o", "/dev/null", "http://vswarm-playwright-"+t.Name+":9222/json/version")
			return checkResult{"playwright cdp reachable for " + t.Name, err == nil, errStr(err)}
		})...)

	pw := make([][]checkResult, len(c.Tenants))
	_ = runParallel(len(c.Tenants), func(i int) error {
		a := c.Tenants[i]
		var rs []checkResult
		for _, b := range c.Tenants {
			if a.Name == b.Name || !b.HasService("postgres") {
				continue
			}
			reach, detail := tenantReachesDB("vswarm-"+a.Name, "vswarm-db-"+b.Name)
			rs = append(rs, checkResult{
				name:   "isolation: " + a.Name + " cannot reach " + b.Name + " db",
				pass:   reach.provesAbsence(),
				detail: detail,
			})
		}
		pw[i] = rs
		return nil
	})
	for _, rs := range pw {
		results = append(results, rs...)
	}

	return results
}

// proxyAnswersPreflight asks the proxy the question a browser engine asks before
// every cross-origin request that carries an Authorization header. It has to be
// answered without an identity, because a preflight carries none.
func proxyAnswersPreflight() (bool, string) {
	out, err := dockerx.Exec(proxyContainer, "curl", "-sS", "-m", "5", "-o", "/dev/null",
		"-w", "%{http_code}", "-X", "OPTIONS",
		"-H", "Origin: https://preflight.invalid",
		"-H", "Access-Control-Request-Method: GET",
		"-H", "Access-Control-Request-Headers: authorization",
		"http://"+render.ProxyIP+":"+render.ProxyPort+"/")
	if err != nil {
		return false, errStr(err)
	}
	code := strings.TrimSpace(out)
	if code != "204" {
		return false, "answered " + code + ", want 204"
	}
	return true, ""
}

// edgeForwardsPreflight checks the half of the preflight path that lives outside
// this repo. The access layer in front of the proxy authenticates by identity, and
// a preflight has none, so an access layer left on its defaults rejects every
// preflight before the proxy can answer it — and no amount of proxy configuration
// shows up as anything but an unreachable workspace.
//
// A host that cannot reach its own public name at all is not evidence either way,
// so that skips rather than fails.
func edgeForwardsPreflight(domain string) checkResult {
	if domain == "" {
		return checkResult{}
	}

	req, err := http.NewRequest(http.MethodOptions, "https://"+domain+"/", nil)
	if err != nil {
		return checkResult{}
	}
	req.Header.Set("Origin", "https://preflight.invalid")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return checkResult{}
	}
	defer resp.Body.Close()

	return classifyEdgePreflight(domain, resp.StatusCode)
}

const edgePreflightCheck = "edge forwards CORS preflights to the proxy"

// classifyEdgePreflight reads a preflight's status the way an operator needs it
// read: the proxy answers 204, so anything else came from in front of it.
func classifyEdgePreflight(domain string, status int) checkResult {
	if status == http.StatusNoContent {
		return checkResult{edgePreflightCheck, true, ""}
	}
	return checkResult{edgePreflightCheck, false, fmt.Sprintf(
		"https://%s answered %d to a preflight; the proxy answers 204, so something in "+
			"front of it is rejecting preflights before they arrive. Let OPTIONS reach "+
			"the origin (Cloudflare Access: options_preflight_bypass).", domain, status)}
}

func anyPublishedPorts() (bool, string) {
	out, err := dockerx.Output("docker", "ps", "--filter", "name=vswarm-", "--format", "{{.Names}} {{.Ports}}")
	if err != nil {
		return false, ""
	}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(ln, "->") {
			return true, strings.TrimSpace(ln)
		}
	}
	return false, ""
}

func tenantReachesProxy(container string) bool {
	_, err := dockerx.Exec(container, "curl", "-sS", "-m", "3", "-o", "/dev/null", "http://vswarm-proxy:8080/")
	return err == nil
}

// probe is a three-valued answer. A check that could not run is not a check
// that ran and found nothing, and conflating the two is how a negative
// assertion ("A cannot reach B") turns a missing tool into a PASS.
//
// This matters beyond the console: Dislace/core gates the fleet converge on
// doctor's exit code (roles/vswarm/tasks/main.yml, `until: vswarm_doctor.rc ==
// 0`), so an isolation claim nobody verified is one an apply will accept.
type probe int

const (
	probeUnknown probe = iota
	probeYes
	probeNo
)

// verified reports whether a negative assertion may be treated as proven.
// probeUnknown must not: it is the absence of evidence.
func (p probe) provesAbsence() bool { return p == probeNo }

// tenantReachesDB answers whether container can open a TCP session to
// dbContainer's postgres port, and says so out of band rather than through an
// exit code, so "the probe could not run" stays distinguishable from "the
// connection was refused". Both used to arrive as a non-zero exit.
func tenantReachesDB(container, dbContainer string) (probe, string) {
	script := fmt.Sprintf(
		"import socket\n"+
			"s = socket.socket()\n"+
			"s.settimeout(3)\n"+
			"try:\n"+
			"    s.connect((%q, 5432))\n"+
			"    s.close()\n"+
			"    print('REACHED')\n"+
			"except OSError as exc:\n"+
			"    print('BLOCKED', exc)\n",
		dbContainer)
	out, err := dockerx.Exec(container, "python3", "-c", script)
	return interpretProbe(out, err, "REACHED", "BLOCKED")
}

// containerHas answers whether a path exists inside a container without
// spending the exit code on the answer, so a stopped container or a missing
// stat is not read as "the file is absent".
func containerHas(container, path string) (probe, string) {
	out, err := dockerx.Exec(container, "sh", "-c",
		"if [ -e "+shellQuote(path)+" ]; then echo PRESENT; else echo ABSENT; fi")
	return interpretProbe(out, err, "PRESENT", "ABSENT")
}

// interpretProbe turns a probe's own words into a verdict. The exit code is
// deliberately not the channel: it cannot separate "the tool is missing" from
// "the answer is no", and every negative assertion in doctor depends on that
// separation.
func interpretProbe(out string, err error, yes, no string) (probe, string) {
	if err != nil {
		return probeUnknown, "probe did not run: " + err.Error()
	}
	switch {
	case strings.Contains(out, yes):
		return probeYes, ""
	case strings.Contains(out, no):
		return probeNo, ""
	default:
		return probeUnknown, "probe produced no verdict: " + strings.TrimSpace(out)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func dbNetworks(dbContainer string) ([]string, error) {
	out, err := dockerx.Output("docker", "inspect", "-f",
		"{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", dbContainer)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func dbNetDetail(nets []string, err error) string {
	if err != nil {
		return err.Error()
	}
	return strings.Join(nets, ",")
}

func tenantTokenAuthenticates(name string) (bool, string) {
	raw, err := os.ReadFile(tokenPath(name))
	if err != nil {
		return false, "no token file"
	}
	_, token := parseTokenFile(string(raw))
	if token == "" {
		return false, "empty token"
	}
	out, err := dockerx.Exec("vswarm-"+name, "curl", "-sS", "-m", "5",
		"-H", "Authorization: Bearer "+token,
		"http://127.0.0.1:3773/api/auth/session")
	if err != nil {
		return false, errStr(err)
	}
	if !strings.Contains(out, `"authenticated":true`) {
		return false, "server rejected token"
	}
	return true, ""
}

// oneLiveSession is the invariant `pair` maintains: the tenant holds a single
// vswarm-owned session, and it is the one angie injects, with life left in it.
// A token that merely authenticates says nothing about how many others are
// still live beside it, or whether this one expires next week.
func oneLiveSession(name string, now time.Time) (bool, string) {
	raw, err := os.ReadFile(tokenPath(name))
	if err != nil {
		return false, "no token file"
	}
	id, _ := parseTokenFile(string(raw))
	if id == "" {
		return false, "token file records no session id"
	}
	live, err := listSessions("vswarm-" + name)
	if err != nil {
		return false, errStr(err)
	}
	stale := staleSessions(live, id)
	if len(stale) > 0 {
		return false, fmt.Sprintf("%d other vswarm sessions still live", len(stale))
	}
	if !reusable(live, id, now) {
		return false, "the injected session is unknown or near expiry"
	}
	return true, ""
}

func adminKeyPath() string {
	return render.HomeDir + "/.ssh/vswarm-admin"
}

func containerMode(container, path string) (string, error) {
	out, err := dockerx.Exec(container, "stat", "-c", "%a", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func cacheMountTook(container string) (bool, string) {
	out, err := dockerx.Exec(container, "cat", "/proc/self/mounts")
	return interpretMounts(out, err)
}

func interpretMounts(out string, err error) (bool, string) {
	return mountedAt(out, err, render.CacheDir, false)
}

func declaredMountTook(container, target string) (bool, string) {
	out, err := dockerx.Exec(container, "cat", "/proc/self/mounts")
	return mountedAt(out, err, target, true)
}

// mountedAt reads /proc/self/mounts for a mount on path. A declared mount is
// only doing its job read-only: writable, it is tenant state on a shared path.
func mountedAt(out string, err error, path string, wantReadOnly bool) (bool, string) {
	if err != nil {
		return false, errStr(err)
	}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 4 || f[1] != path {
			continue
		}
		if wantReadOnly && !hasOption(f[3], "ro") {
			return false, "mount at " + path + " is writable"
		}
		return true, ""
	}
	return false, "no mount at " + path
}

func hasOption(options, want string) bool {
	for _, o := range strings.Split(options, ",") {
		if o == want {
			return true
		}
	}
	return false
}

func pathDetail(mode string, err error) string {
	if err != nil {
		return err.Error()
	}
	return mode
}

func detailSuffix(d string) string {
	if strings.TrimSpace(d) == "" {
		return ""
	}
	return "  (" + strings.TrimSpace(d) + ")"
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
