package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func cmdDoctor() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	var results []checkResult
	check := func(name string, pass bool, detail string) {
		results = append(results, checkResult{name, pass, detail})
	}

	_, statErr := os.Stat("generated/docker-compose.yml")
	check("rendered compose present", statErr == nil, "")

	_, aerr := dockerx.Exec(proxyContainer, "angie", "-t")
	check("angie -t config valid", aerr == nil, errStr(aerr))

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
			_, err := containerMode("vswarm-"+t.Name, adminKeyPath())
			return checkResult{"no admin key in non-admin home: " + t.Name, err != nil, adminKeyDetail(err)}
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

	pw := make([][]checkResult, len(c.Tenants))
	_ = runParallel(len(c.Tenants), func(i int) error {
		a := c.Tenants[i]
		var rs []checkResult
		for _, b := range c.Tenants {
			if a.Name == b.Name || !b.HasService("postgres") {
				continue
			}
			reach := tenantReachesDB("vswarm-"+a.Name, "vswarm-db-"+b.Name)
			rs = append(rs, checkResult{
				name: "isolation: " + a.Name + " cannot reach " + b.Name + " db",
				pass: !reach,
			})
		}
		pw[i] = rs
		return nil
	})
	for _, rs := range pw {
		results = append(results, rs...)
	}

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

func tenantReachesDB(container, dbContainer string) bool {
	script := fmt.Sprintf(
		"import socket; s=socket.socket(); s.settimeout(3); s.connect((%q, 5432)); s.close()",
		dbContainer)
	_, err := dockerx.Exec(container, "python3", "-c", script)
	return err == nil
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
	p := filepath.Join(render.GeneratedDir, "angie", "tenants", name+".token")
	raw, err := os.ReadFile(p)
	if err != nil {
		return false, "no token file"
	}
	token := tokenFromLine(string(raw))
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

func tokenFromLine(s string) string {
	fields := strings.Split(s, `"`)
	if len(fields) < 4 {
		return ""
	}
	return fields[3]
}

func adminKeyPath() string {
	return render.HomeDir + "/.ssh/vswarm-admin"
}

func adminKeyDetail(err error) string {
	if err == nil {
		return "present"
	}
	return ""
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
	if err != nil {
		return false, errStr(err)
	}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[1] == render.CacheDir {
			return true, ""
		}
	}
	return false, "no mount at " + render.CacheDir
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
