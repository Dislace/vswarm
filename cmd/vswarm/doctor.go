package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dislace/vswarm/internal/dockerx"
	"github.com/dislace/vswarm/internal/render"
)

func cmdDoctor() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	ok := true
	check := func(name string, pass bool, detail string) {
		mark := "PASS"
		if !pass {
			mark, ok = "FAIL", false
		}
		fmt.Printf("[%s] %s%s\n", mark, name, detailSuffix(detail))
	}

	_, statErr := os.Stat("generated/docker-compose.yml")
	check("rendered compose present", statErr == nil, "")

	_, aerr := dockerx.Exec(proxyContainer, "angie", "-t")
	check("angie -t config valid", aerr == nil, errStr(aerr))

	published, detail := anyPublishedPorts()
	check("no published host ports", !published, detail)

	for _, t := range c.Tenants {
		reach := tenantReachesProxy("vswarm-" + t.Name)
		check("isolation: "+t.Name+" cannot reach proxy", !reach, "")
	}

	for _, t := range c.Tenants {
		authed, detail := tenantTokenAuthenticates(t.Name)
		check("token authenticates for "+t.Name, authed, detail)
	}

	for _, t := range c.Tenants {
		for _, vol := range []string{render.WorkVolume(t.Name), render.CacheVolume(t.Name)} {
			_, verr := dockerx.Output("docker", "volume", "inspect", vol)
			check("volume present: "+vol, verr == nil, errStr(verr))
		}
	}

	// The split is only real if the two paths are on different filesystems.
	// Comparing device numbers proves the cache mount actually took, which a
	// glance at the compose file cannot: a typo'd mount path silently lands
	// the cache back inside the work volume and nothing else would notice.
	for _, t := range c.Tenants {
		same, detail := sameDevice("vswarm-"+t.Name, render.HomeDir, render.CacheDir)
		check("cache is a separate volume for "+t.Name, !same, detail)
	}

	for _, t := range c.Tenants {
		mode, merr := containerMode("vswarm-"+t.Name, render.HomeDir+"/.ssh")
		check("ssh perms 700 for "+t.Name, merr == nil && mode == "700", pathDetail(mode, merr))
	}

	for _, t := range c.Tenants {
		if t.Admin {
			continue
		}
		_, err := containerMode("vswarm-"+t.Name, adminKeyPath())
		check("no admin key in non-admin home: "+t.Name, err != nil, adminKeyDetail(err))
	}

	for _, t := range c.Tenants {
		if !t.Admin {
			continue
		}
		mode, merr := containerMode("vswarm-"+t.Name, adminKeyPath())
		check("admin key 0600 for "+t.Name, merr == nil && mode == "600", pathDetail(mode, merr))
	}

	for _, t := range c.Tenants {
		if !t.HasService("postgres") {
			continue
		}
		nets, nerr := dbNetworks("vswarm-db-" + t.Name)
		want := "vswarm-net-" + t.Name
		onlyOwn := nerr == nil && len(nets) == 1 && nets[0] == want
		check("db "+t.Name+" on exactly its network", onlyOwn, dbNetDetail(nets, nerr))
	}

	for _, a := range c.Tenants {
		for _, b := range c.Tenants {
			if a.Name == b.Name || !b.HasService("postgres") {
				continue
			}
			reach := tenantReachesDB("vswarm-"+a.Name, "vswarm-db-"+b.Name)
			check("isolation: "+a.Name+" cannot reach "+b.Name+" db", !reach, "")
		}
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

// tenantReachesDB attempts a real TCP connect to a db container's postgres port
// from inside another tenant's workspace. curl-to-postgres always errors on the
// HTTP handshake even when connected, so it cannot prove reachability; python3
// (present in the workspace image) opens the socket and exits 0 iff it connects.
func tenantReachesDB(container, dbContainer string) bool {
	script := fmt.Sprintf(
		"import socket; s=socket.socket(); s.settimeout(3); s.connect((%q, 5432)); s.close()",
		dbContainer)
	_, err := dockerx.Exec(container, "python3", "-c", script)
	return err == nil
}

// dbNetworks returns the docker networks a db container is attached to.
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

// containerMode reads a permission bitmask from inside the workspace. The home
// is a named volume now, so the host has no path to stat — and asking the
// container is stricter anyway: it checks what the tenant actually sees rather
// than what the deployment layer believes it wrote.
func containerMode(container, path string) (string, error) {
	out, err := dockerx.Exec(container, "stat", "-c", "%a", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func sameDevice(container, a, b string) (bool, string) {
	out, err := dockerx.Exec(container, "stat", "-c", "%d", a, b)
	if err != nil {
		return false, errStr(err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return false, "unexpected stat output: " + strings.TrimSpace(out)
	}
	if fields[0] == fields[1] {
		return true, "both on device " + fields[0]
	}
	return false, ""
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
