package render

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/templates"
)

const (
	GeneratedDir = "generated"
	T3Port       = "3773"
	ProxyPort    = "8080"
	EdgeSubnet   = "172.31.0.0/24"
	PGPort       = "5432"
	// DBMemory caps every postgres sidecar (policy, not per-tenant tunable).
	DBMemory = "1g"
)

type tenantView struct {
	Name      string
	Email     string
	Container string
	Net       string
	Subnet    string

	Postgres    bool
	DBContainer string
	DBVolume    string
	PGUser      string
	PGDatabase  string
	PGPassword  string
}

type view struct {
	Domain       string
	Image        string
	DBImage      string
	DBMemory     string
	Team         string
	CPUs         string
	Memory       string
	Pids         int
	EdgeSubnet   string
	ProxyPort    string
	T3Port       string
	ManageTunnel bool
	EdgeExternal bool
	AnyPostgres  bool
	Tenants      []tenantView
}

func buildView(c *config.Config) view {
	team := c.Team
	if team == "" {
		team = strings.SplitN(c.Domain, ".", 2)[0]
	}
	v := view{
		Domain:       c.Domain,
		Image:        c.Image,
		DBImage:      c.DBImage,
		DBMemory:     DBMemory,
		Team:         team,
		CPUs:         c.Resources.CPUs,
		Memory:       c.Resources.Memory,
		Pids:         c.Resources.Pids,
		EdgeSubnet:   EdgeSubnet,
		ProxyPort:    ProxyPort,
		T3Port:       T3Port,
		ManageTunnel: c.ManageTunnel,
		EdgeExternal: c.EdgeExternal,
	}
	for i, t := range c.Tenants {
		tv := tenantView{
			Name:      t.Name,
			Email:     t.Email,
			Container: "vswarm-" + t.Name,
			Net:       "vswarm-net-" + t.Name,
			Subnet:    fmt.Sprintf("172.31.%d.0/24", 10+i),
		}
		if t.HasService("postgres") {
			tv.Postgres = true
			tv.DBContainer = "vswarm-db-" + t.Name
			tv.DBVolume = "vswarm-dbdata-" + t.Name
			tv.PGUser = "postgres"
			tv.PGDatabase = "postgres"
			v.AnyPostgres = true
		}
		v.Tenants = append(v.Tenants, tv)
	}
	return v
}

func Render(c *config.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	v := buildView(c)

	for _, d := range []string{
		GeneratedDir,
		filepath.Join(GeneratedDir, "angie"),
		filepath.Join(GeneratedDir, "angie", "tenants"),
		filepath.Join(GeneratedDir, "image"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	for i := range v.Tenants {
		if !v.Tenants[i].Postgres {
			continue
		}
		pw, err := resolvePGPassword(v.Tenants[i].Name)
		if err != nil {
			return err
		}
		v.Tenants[i].PGPassword = pw
		if err := writePGEnv(v.Tenants[i]); err != nil {
			return err
		}
	}

	files := []struct {
		tmpl, dst string
		mode      os.FileMode
	}{
		{"docker-compose.yml.tmpl", filepath.Join(GeneratedDir, "docker-compose.yml"), 0o644},
		{"angie.conf.tmpl", filepath.Join(GeneratedDir, "angie", "angie.conf"), 0o644},
		{"Dockerfile.tmpl", filepath.Join(GeneratedDir, "image", "Dockerfile"), 0o644},
		{"entrypoint.sh.tmpl", filepath.Join(GeneratedDir, "image", "entrypoint.sh"), 0o755},
		{"prompt.sh.tmpl", filepath.Join(GeneratedDir, "image", "prompt.sh"), 0o644},
	}
	for _, f := range files {
		if err := renderTemplate(f.tmpl, f.dst, v, f.mode); err != nil {
			return fmt.Errorf("render %s: %w", f.tmpl, err)
		}
	}

	want := map[string]bool{}
	for _, t := range v.Tenants {
		want[t.Name] = true
		line := fmt.Sprintf("%q %q;\n", t.Email, t.Container+":"+T3Port)
		p := filepath.Join(GeneratedDir, "angie", "tenants", t.Name+".upstream")
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			return err
		}
	}
	matches, _ := filepath.Glob(filepath.Join(GeneratedDir, "angie", "tenants", "*.upstream"))
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".upstream")
		if !want[name] {
			os.Remove(m)
			os.Remove(filepath.Join(GeneratedDir, "angie", "tenants", name+".token"))
		}
	}
	return nil
}

// resolvePGPassword returns the tenant's persisted postgres password, minting a
// fresh one only when no prior ~/.pg.env exists — re-renders never rotate it.
func resolvePGPassword(name string) (string, error) {
	if raw, err := os.ReadFile(pgEnvPath(name)); err == nil {
		if pw := pgEnvValue(string(raw), "PGPASSWORD"); pw != "" {
			return pw, nil
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writePGEnv delivers the connection contract into the tenant home (mode 0600,
// uid 1000 when running as root — the same ownership model as .infisical.env).
func writePGEnv(t tenantView) error {
	p := pgEnvPath(t.Name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("PGHOST=%s\nPGPORT=%s\nPGUSER=%s\nPGPASSWORD=%s\nPGDATABASE=%s\n",
		t.DBContainer, PGPort, t.PGUser, t.PGPassword, t.PGDatabase)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(p, 1000, 1000); err != nil {
			return err
		}
	}
	return nil
}

func pgEnvPath(name string) string {
	return filepath.Join("config", name, "home", ".pg.env")
}

func pgEnvValue(env, key string) string {
	for _, ln := range strings.Split(env, "\n") {
		if k, v, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func renderTemplate(name, dst string, v view, mode os.FileMode) error {
	raw, err := templates.FS.ReadFile(name)
	if err != nil {
		return err
	}
	t, err := template.New(name).Parse(string(raw))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := t.Execute(f, v); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
