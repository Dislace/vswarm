package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Tenant struct {
	Email string
	Name  string
}

type Resources struct {
	CPUs   string
	Memory string
	Pids   int
}

type Config struct {
	Domain       string
	Image        string
	Resources    Resources
	TokenTTL     string
	ManageTunnel bool
	EdgeExternal bool
	Tenants      []Tenant

	Path string
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func Default() *Config {
	return &Config{
		Image:        "vswarm/workspace:latest",
		Resources:    Resources{CPUs: "2.0", Memory: "6g", Pids: 512},
		TokenTTL:     "30d",
		ManageTunnel: true,
	}
}

func Parse(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := Default()
	c.Path = path
	section := ""
	for n, raw := range strings.Split(string(data), "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trim := strings.TrimSpace(line)

		if indent == 0 {
			key, val := splitKV(trim)
			switch key {
			case "domain":
				c.Domain = unquote(val)
				section = ""
			case "image":
				if val != "" {
					c.Image = unquote(val)
				}
				section = ""
			case "token_ttl":
				if val != "" {
					c.TokenTTL = unquote(val)
				}
				section = ""
			case "manage_tunnel":
				c.ManageTunnel = parseBool(val)
				section = ""
			case "edge_external":
				c.EdgeExternal = parseBool(val)
				section = ""
			case "resources":
				section = "resources"
			case "tenants":
				section = "tenants"
			default:
				return nil, fmt.Errorf("%s:%d: unknown key %q", path, n+1, key)
			}
			continue
		}

		switch section {
		case "resources":
			key, val := splitKV(trim)
			switch key {
			case "cpus":
				c.Resources.CPUs = unquote(val)
			case "memory":
				c.Resources.Memory = unquote(val)
			case "pids":
				if p, err := strconv.Atoi(unquote(val)); err == nil {
					c.Resources.Pids = p
				}
			}
		case "tenants":
			if strings.HasPrefix(trim, "-") {
				c.Tenants = append(c.Tenants, Tenant{})
				rest := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
				if rest != "" {
					k, v := splitKV(rest)
					applyTenant(&c.Tenants[len(c.Tenants)-1], k, v)
				}
			} else if len(c.Tenants) > 0 {
				k, v := splitKV(trim)
				applyTenant(&c.Tenants[len(c.Tenants)-1], k, v)
			}
		}
	}
	return c, nil
}

func applyTenant(t *Tenant, k, v string) {
	switch k {
	case "email":
		t.Email = unquote(v)
	case "name":
		t.Name = unquote(v)
	}
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Domain) == "" {
		return fmt.Errorf("domain is required")
	}
	seenName := map[string]bool{}
	seenEmail := map[string]bool{}
	for _, t := range c.Tenants {
		if !strings.Contains(t.Email, "@") {
			return fmt.Errorf("tenant %q: invalid email %q", t.Name, t.Email)
		}
		if !nameRe.MatchString(t.Name) {
			return fmt.Errorf("tenant name %q must be DNS-safe [a-z0-9-]", t.Name)
		}
		if seenName[t.Name] {
			return fmt.Errorf("duplicate tenant name %q", t.Name)
		}
		if seenEmail[t.Email] {
			return fmt.Errorf("duplicate tenant email %q", t.Email)
		}
		seenName[t.Name] = true
		seenEmail[t.Email] = true
	}
	return nil
}

func (c *Config) Tenant(name string) (Tenant, bool) {
	for _, t := range c.Tenants {
		if t.Name == name {
			return t, true
		}
	}
	return Tenant{}, false
}

func (c *Config) AddTenant(email, name string) error {
	c.Tenants = append(c.Tenants, Tenant{Email: email, Name: name})
	return c.Validate()
}

func (c *Config) RemoveTenant(name string) bool {
	out := c.Tenants[:0]
	found := false
	for _, t := range c.Tenants {
		if t.Name == name {
			found = true
			continue
		}
		out = append(out, t)
	}
	c.Tenants = out
	return found
}

func (c *Config) Save() error {
	var b strings.Builder
	b.WriteString("# VibeSwarm tenant manifest (managed by `vswarm`).\n")
	fmt.Fprintf(&b, "domain: %s\n", c.Domain)
	fmt.Fprintf(&b, "image: %s\n", c.Image)
	b.WriteString("resources:\n")
	fmt.Fprintf(&b, "  cpus: \"%s\"\n", c.Resources.CPUs)
	fmt.Fprintf(&b, "  memory: %s\n", c.Resources.Memory)
	fmt.Fprintf(&b, "  pids: %d\n", c.Resources.Pids)
	fmt.Fprintf(&b, "token_ttl: %s\n", c.TokenTTL)
	fmt.Fprintf(&b, "manage_tunnel: %t\n", c.ManageTunnel)
	fmt.Fprintf(&b, "edge_external: %t\n", c.EdgeExternal)
	b.WriteString("tenants:\n")
	for _, t := range c.Tenants {
		fmt.Fprintf(&b, "  - email: %s\n", t.Email)
		fmt.Fprintf(&b, "    name: %s\n", t.Name)
	}
	return os.WriteFile(c.Path, []byte(b.String()), 0o644)
}

func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

func splitKV(s string) (string, string) {
	i := strings.Index(s, ":")
	if i < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
}

func parseBool(s string) bool {
	return strings.EqualFold(strings.TrimSpace(unquote(s)), "true")
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
