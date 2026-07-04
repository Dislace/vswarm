package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/dislace/vibeswarm/internal/config"
	"github.com/dislace/vibeswarm/templates"
)

const (
	GeneratedDir = "generated"
	T3Port       = "3773"
	ProxyPort    = "8080"
	EdgeSubnet   = "172.31.0.0/24"
	ProxyIP      = "172.31.0.2"
)

type tenantView struct {
	Name      string
	Email     string
	Container string
	Net       string
	Subnet    string
}

type view struct {
	Domain       string
	Image        string
	CPUs         string
	Memory       string
	Pids         int
	EdgeSubnet   string
	ProxyIP      string
	ProxyPort    string
	T3Port       string
	ManageTunnel bool
	EdgeExternal bool
	Tenants      []tenantView
}

func buildView(c *config.Config) view {
	v := view{
		Domain:       c.Domain,
		Image:        c.Image,
		CPUs:         c.Resources.CPUs,
		Memory:       c.Resources.Memory,
		Pids:         c.Resources.Pids,
		EdgeSubnet:   EdgeSubnet,
		ProxyIP:      ProxyIP,
		ProxyPort:    ProxyPort,
		T3Port:       T3Port,
		ManageTunnel: c.ManageTunnel,
		EdgeExternal: c.EdgeExternal,
	}
	for i, t := range c.Tenants {
		v.Tenants = append(v.Tenants, tenantView{
			Name:      t.Name,
			Email:     t.Email,
			Container: "vswarm-" + t.Name,
			Net:       "vswarm-net-" + t.Name,
			Subnet:    fmt.Sprintf("172.31.%d.0/24", 10+i),
		})
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

	files := []struct {
		tmpl, dst string
		mode      os.FileMode
	}{
		{"docker-compose.yml.tmpl", filepath.Join(GeneratedDir, "docker-compose.yml"), 0o644},
		{"angie.conf.tmpl", filepath.Join(GeneratedDir, "angie", "angie.conf"), 0o644},
		{"Dockerfile.tmpl", filepath.Join(GeneratedDir, "image", "Dockerfile"), 0o644},
		{"entrypoint.sh.tmpl", filepath.Join(GeneratedDir, "image", "entrypoint.sh"), 0o755},
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
