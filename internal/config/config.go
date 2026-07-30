package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Tenant struct {
	Email    string
	Name     string
	Services []string
	Admin    bool

	Repos []string
}

func (t Tenant) HasService(name string) bool {
	for _, s := range t.Services {
		if s == name {
			return true
		}
	}
	return false
}

type Resources struct {
	CPUs   string
	Memory string
	Pids   int
}

type Storage struct {
	Driver string
	Opts   map[string]string
}

type Config struct {
	Domain       string
	Image        string
	ImageOverlay string
	DBImage      string
	Team         string
	RepoBase     string
	Resources    Resources
	Storage      Storage
	TokenTTL     string
	ManageTunnel bool
	EdgeExternal bool
	Tenants      []Tenant

	Path string
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

var knownServices = map[string]bool{"postgres": true}

func Default() *Config {
	return &Config{
		Image:        "vswarm/workspace:latest",
		DBImage:      "timescale/timescaledb:2.28.2-pg17",
		Resources:    Resources{CPUs: "2.0", Memory: "6g", Pids: 4096},
		RepoBase:     "git@github.com:",
		Storage:      Storage{Driver: "local", Opts: map[string]string{}},
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
			case "image_overlay":
				c.ImageOverlay = unquote(val)
				section = ""
			case "db_image":
				if val != "" {
					c.DBImage = unquote(val)
				}
				section = ""
			case "team":
				c.Team = unquote(val)
				section = ""
			case "repo_base":
				if val != "" {
					c.RepoBase = unquote(val)
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
			case "storage":
				section = "storage"
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
		case "storage":
			key, val := splitKV(trim)
			switch {
			case key == "driver":
				if val != "" {
					c.Storage.Driver = unquote(val)
				}
			case strings.HasPrefix(key, "opt."):
				c.Storage.Opts[strings.TrimPrefix(key, "opt.")] = unquote(val)
			default:
				return nil, fmt.Errorf("%s:%d: unknown storage key %q", path, n+1, key)
			}
		case "tenants":
			if strings.HasPrefix(trim, "-") {
				c.Tenants = append(c.Tenants, Tenant{})
				rest := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
				if rest != "" {
					k, v := splitKV(rest)
					if err := applyTenant(&c.Tenants[len(c.Tenants)-1], k, v); err != nil {
						return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
					}
				}
			} else if len(c.Tenants) > 0 {
				k, v := splitKV(trim)
				if err := applyTenant(&c.Tenants[len(c.Tenants)-1], k, v); err != nil {
					return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
				}
			}
		}
	}
	return c, nil
}

func applyTenant(t *Tenant, k, v string) error {
	switch k {
	case "email":
		t.Email = unquote(v)
	case "name":
		t.Name = unquote(v)
	case "services":
		for _, s := range parseList(v) {
			if !knownServices[s] {
				return fmt.Errorf("unknown service %q", s)
			}
			t.Services = append(t.Services, s)
		}
	case "admin":
		t.Admin = parseBool(v)
	case "repos":
		t.Repos = append(t.Repos, parseList(v)...)
	}
	return nil
}

func (c *Config) RepoURL(entry string) string {
	if strings.Contains(entry, "://") || strings.Contains(entry, "@") {
		return entry
	}
	return c.RepoBase + entry
}

func RepoDir(entry string) string {
	s := strings.TrimSuffix(entry, ".git")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Domain) == "" {
		return fmt.Errorf("domain is required")
	}
	if strings.TrimSpace(c.Storage.Driver) == "" {
		c.Storage.Driver = "local"
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
	if c.ImageOverlay != "" {
		fmt.Fprintf(&b, "image_overlay: %s\n", c.ImageOverlay)
	}
	if c.DBImage != "" {
		fmt.Fprintf(&b, "db_image: %s\n", c.DBImage)
	}
	if c.Team != "" {
		fmt.Fprintf(&b, "team: %s\n", c.Team)
	}
	if c.RepoBase != "" {
		fmt.Fprintf(&b, "repo_base: %q\n", c.RepoBase)
	}
	b.WriteString("resources:\n")
	fmt.Fprintf(&b, "  cpus: \"%s\"\n", c.Resources.CPUs)
	fmt.Fprintf(&b, "  memory: %s\n", c.Resources.Memory)
	fmt.Fprintf(&b, "  pids: %d\n", c.Resources.Pids)
	if c.Storage.Driver != "" && c.Storage.Driver != "local" || len(c.Storage.Opts) > 0 {
		b.WriteString("storage:\n")
		fmt.Fprintf(&b, "  driver: %s\n", c.Storage.Driver)
		for _, k := range sortedKeys(c.Storage.Opts) {
			fmt.Fprintf(&b, "  opt.%s: %q\n", k, c.Storage.Opts[k])
		}
	}
	fmt.Fprintf(&b, "token_ttl: %s\n", c.TokenTTL)
	fmt.Fprintf(&b, "manage_tunnel: %t\n", c.ManageTunnel)
	fmt.Fprintf(&b, "edge_external: %t\n", c.EdgeExternal)
	b.WriteString("tenants:\n")
	for _, t := range c.Tenants {
		fmt.Fprintf(&b, "  - email: %s\n", t.Email)
		fmt.Fprintf(&b, "    name: %s\n", t.Name)
		if len(t.Services) > 0 {
			fmt.Fprintf(&b, "    services: [%s]\n", strings.Join(t.Services, ", "))
		}
		if t.Admin {
			b.WriteString("    admin: true\n")
		}
		if len(t.Repos) > 0 {
			fmt.Fprintf(&b, "    repos: [%s]\n", strings.Join(t.Repos, ", "))
		}
	}
	return os.WriteFile(c.Path, []byte(b.String()), 0o644)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

func parseList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = unquote(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
