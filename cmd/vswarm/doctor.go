package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dislace/vibeswarm/internal/dockerx"
	"github.com/dislace/vibeswarm/internal/render"
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
		p := filepath.Join("config", t.Name, "home", ".ssh")
		mode, merr := dirMode(p)
		check("ssh perms 700 for "+t.Name, merr == nil && mode == 0o700, modeStr(mode, merr))
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

func dirMode(p string) (os.FileMode, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Mode().Perm(), nil
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

func modeStr(m os.FileMode, err error) string {
	if err != nil {
		return err.Error()
	}
	return m.String()
}
