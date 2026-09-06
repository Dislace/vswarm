package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	// NetID is the third octet of the tenant's bridge subnet. Zero means the
	// roster has not declared one and render falls back to roster position.
	// core/infra source-pins each admin key to this subnet, so deriving it
	// from position moved an access-control boundary whenever a tenant was
	// removed.
	NetID int

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

// Mount is a host path published read-only into every workspace container.
// Host-managed assets belong here rather than in the image: rebaking them
// moves the image id, and a moved image id recreates every workspace.
type Mount struct {
	Source string
	Target string
}

type Storage struct {
	Driver string
	Opts   map[string]string
}

type Config struct {
	Domain          string
	Image           string
	DBImage         string
	PlaywrightImage string
	Team            string
	RepoBase        string
	Resources       Resources
	Storage         Storage
	TokenTTL        string
	ManageTunnel    bool
	EdgeExternal    bool
	Mounts          []Mount
	Tenants         []Tenant

	Path string
}

// Container paths the workspace service already occupies. They live here
// because mount validation has to keep declared mounts off them, and the
// compose template renders from the same constants so the two cannot drift.
const (
	HomeDir  = "/home/ai-agent"
	CacheDir = HomeDir + "/.cache"
	RunDir   = "/run"
)

// ReservedTargets is every container path a workspace mounts on its own. A
// declared mount may not take one, shadow one, or sit under one.
var ReservedTargets = []string{HomeDir, CacheDir, RunDir}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

var knownServices = map[string]bool{"postgres": true, "playwright": true}

func Default() *Config {
	return &Config{
		DBImage:         "postgres:18.4",
		PlaywrightImage: "zenika/alpine-chrome:124",
		Resources:       Resources{CPUs: "2.0", Memory: "6g", Pids: 4096},
		RepoBase:        "git@github.com:",
		Storage:         Storage{Driver: "local", Opts: map[string]string{}},
		TokenTTL:        "30d",
		ManageTunnel:    true,
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
			case "db_image":
				if val != "" {
					c.DBImage = unquote(val)
				}
				section = ""
			case "playwright_image":
				if val != "" {
					c.PlaywrightImage = unquote(val)
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
			case "mounts":
				section = "mounts"
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
				p, err := strconv.Atoi(unquote(val))
				if err != nil {
					return nil, fmt.Errorf("%s:%d: invalid pids %q: %w", path, n+1, val, err)
				}
				c.Resources.Pids = p
			default:
				return nil, fmt.Errorf("%s:%d: unknown resources key %q", path, n+1, key)
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
		case "mounts":
			if !strings.HasPrefix(trim, "-") {
				return nil, fmt.Errorf("%s:%d: mounts takes a list of source:target entries", path, n+1)
			}
			m, err := parseMount(strings.TrimSpace(strings.TrimPrefix(trim, "-")))
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
			}
			c.Mounts = append(c.Mounts, m)
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
		default:
			return nil, fmt.Errorf("%s:%d: unexpected indented key %q", path, n+1, trim)
		}
	}
	return c, nil
}

func parseMount(entry string) (Mount, error) {
	source, target, ok := strings.Cut(unquote(entry), ":")
	if !ok {
		return Mount{}, fmt.Errorf("mount %q must be source:target", entry)
	}
	return Mount{Source: strings.TrimSpace(source), Target: strings.TrimSpace(target)}, nil
}

// Tenant bridge subnets are 172.31.<net_id>.0/24. 0-9 are reserved for the
// edge network and its proxy; 255 is the broadcast address.
const (
	MinNetID = 10
	MaxNetID = 254
)

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
	case "net_id":
		id, err := strconv.Atoi(strings.TrimSpace(unquote(v)))
		if err != nil {
			return fmt.Errorf("net_id %q is not a number", v)
		}
		t.NetID = id
	case "repos":
		t.Repos = append(t.Repos, parseList(v)...)
	default:
		return fmt.Errorf("unknown tenant key %q", k)
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
	// The image is built and published somewhere else, so there is no
	// defensible default: a host that names none would silently run whatever
	// `vswarm/workspace:latest` happens to mean on that machine.
	if strings.TrimSpace(c.Image) == "" {
		return fmt.Errorf("image is required — name the published workspace image")
	}
	if strings.TrimSpace(c.Storage.Driver) == "" {
		c.Storage.Driver = "local"
	}
	seenTarget := map[string]bool{}
	for _, m := range c.Mounts {
		for _, p := range []string{m.Source, m.Target} {
			// Canonical rules out "..", "." and "//", so the checks below
			// cannot be walked around by spelling the same path differently.
			if !strings.HasPrefix(p, "/") || filepath.Clean(p) != p {
				return fmt.Errorf("mount path %q must be absolute and canonical", p)
			}
			// A path reaches the compose file verbatim, where "$" would
			// interpolate from the deploying environment.
			if strings.ContainsAny(p, "\"\\\n#:$") {
				return fmt.Errorf("mount path %q contains unsupported characters", p)
			}
		}
		for _, reserved := range ReservedTargets {
			if within(m.Target, reserved) || within(reserved, m.Target) {
				return fmt.Errorf("mount %s: the workspace already mounts %s", m.Target, reserved)
			}
		}
		if seenTarget[m.Target] {
			return fmt.Errorf("duplicate mount target %q", m.Target)
		}
		seenTarget[m.Target] = true
	}
	seenName := map[string]bool{}
	seenEmail := map[string]bool{}
	seenNetID := map[int]string{}
	for _, t := range c.Tenants {
		if !strings.Contains(t.Email, "@") {
			return fmt.Errorf("tenant %q: invalid email %q", t.Name, t.Email)
		}
		if strings.ContainsAny(t.Email, "\"\\\n#") {
			return fmt.Errorf("tenant %q: email contains unsupported characters", t.Name)
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
		if t.NetID != 0 {
			if t.NetID < MinNetID || t.NetID > MaxNetID {
				return fmt.Errorf("tenant %q: net_id %d is outside %d-%d",
					t.Name, t.NetID, MinNetID, MaxNetID)
			}
			if other, taken := seenNetID[t.NetID]; taken {
				return fmt.Errorf("tenants %q and %q both declare net_id %d; "+
					"each subnet authorizes one tenant's keys", other, t.Name, t.NetID)
			}
			seenNetID[t.NetID] = t.Name
		}
		seenName[t.Name] = true
		seenEmail[t.Email] = true
	}
	return nil
}

// within reports whether path is dir or sits under it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
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
	if err := c.Validate(); err != nil {
		return err
	}
	return c.AssignNetIDs()
}

// AssignNetIDs gives a net_id to every tenant that declared none, preferring the
// octet its roster position already renders so adopting this renumbers nothing.
// A declared id is never moved: keeping it across roster edits is the point.
func (c *Config) AssignNetIDs() error {
	taken := map[int]bool{}
	for _, t := range c.Tenants {
		if t.NetID != 0 {
			taken[t.NetID] = true
		}
	}
	for i := range c.Tenants {
		if c.Tenants[i].NetID != 0 {
			continue
		}
		id, err := freeNetID(taken, i)
		if err != nil {
			return fmt.Errorf("tenant %q: %w", c.Tenants[i].Name, err)
		}
		c.Tenants[i].NetID = id
		taken[id] = true
	}
	return nil
}

func freeNetID(taken map[int]bool, position int) (int, error) {
	if want := MinNetID + position; want <= MaxNetID && !taken[want] {
		return want, nil
	}
	for id := MinNetID; id <= MaxNetID; id++ {
		if !taken[id] {
			return id, nil
		}
	}
	return 0, fmt.Errorf("no free net_id in %d-%d", MinNetID, MaxNetID)
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
	if c.DBImage != "" {
		fmt.Fprintf(&b, "db_image: %s\n", c.DBImage)
	}
	if c.PlaywrightImage != "" {
		fmt.Fprintf(&b, "playwright_image: %s\n", c.PlaywrightImage)
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
	if len(c.Mounts) > 0 {
		b.WriteString("mounts:\n")
		for _, m := range c.Mounts {
			fmt.Fprintf(&b, "  - %s:%s\n", m.Source, m.Target)
		}
	}
	b.WriteString("tenants:\n")
	for _, t := range c.Tenants {
		fmt.Fprintf(&b, "  - email: %q\n", t.Email)
		fmt.Fprintf(&b, "    name: %s\n", t.Name)
		if len(t.Services) > 0 {
			fmt.Fprintf(&b, "    services: [%s]\n", strings.Join(t.Services, ", "))
		}
		if t.Admin {
			b.WriteString("    admin: true\n")
		}
		if t.NetID != 0 {
			fmt.Fprintf(&b, "    net_id: %d\n", t.NetID)
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
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
				return s[:i]
			}
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
