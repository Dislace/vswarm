package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidateAndSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	input := `domain: code.example.com
image: registry.example.com/vswarm:v1
db_image: registry.example.com/timescaledb:pg17
team: platform
resources:
  cpus: "3.5"
  memory: 8g
  pids: 2048
token_ttl: 12h
manage_tunnel: false
edge_external: true
tenants:
  - email: alice@example.com
    name: alice
    services: [postgres]
    admin: true
  - email: bob@example.com
    name: bob-dev
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Domain != "code.example.com" || got.Image != "registry.example.com/vswarm:v1" {
		t.Fatalf("unexpected top-level config: %#v", got)
	}
	if got.Resources.CPUs != "3.5" || got.Resources.Memory != "8g" || got.Resources.Pids != 2048 {
		t.Fatalf("unexpected resources: %#v", got.Resources)
	}
	if got.ManageTunnel || !got.EdgeExternal {
		t.Fatalf("unexpected network flags: manage=%t external=%t", got.ManageTunnel, got.EdgeExternal)
	}
	if len(got.Tenants) != 2 || got.Tenants[1].Name != "bob-dev" {
		t.Fatalf("unexpected tenants: %#v", got.Tenants)
	}
	if got.DBImage != "registry.example.com/timescaledb:pg17" {
		t.Fatalf("unexpected db_image: %q", got.DBImage)
	}
	if !got.Tenants[0].HasService("postgres") || got.Tenants[1].HasService("postgres") {
		t.Fatalf("unexpected services: %#v", got.Tenants)
	}
	if !got.Tenants[0].Admin || got.Tenants[1].Admin {
		t.Fatalf("unexpected admin flags: %#v", got.Tenants)
	}

	if err := got.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	roundTrip, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse(saved config) error = %v", err)
	}
	if err := roundTrip.Validate(); err != nil {
		t.Fatalf("Validate(saved config) error = %v", err)
	}
	if roundTrip.Domain != got.Domain ||
		roundTrip.Image != got.Image ||
		roundTrip.DBImage != got.DBImage ||
		roundTrip.Team != got.Team ||
		roundTrip.Resources != got.Resources ||
		roundTrip.TokenTTL != got.TokenTTL ||
		roundTrip.ManageTunnel != got.ManageTunnel ||
		roundTrip.EdgeExternal != got.EdgeExternal ||
		len(roundTrip.Tenants) != len(got.Tenants) ||
		!roundTrip.Tenants[0].HasService("postgres") ||
		roundTrip.Tenants[0].Admin != got.Tenants[0].Admin ||
		roundTrip.Tenants[1].Admin != got.Tenants[1].Admin {
		t.Fatalf("round trip mismatch:\nwant %#v\ngot  %#v", got, roundTrip)
	}
}

func TestParseRejectsUnknownService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	in := "domain: code.example.com\ntenants:\n  - email: a@example.com\n    name: a\n    services: [postgres, mongo]\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), `unknown service "mongo"`) {
		t.Fatalf("Parse() error = %v, want unknown service", err)
	}
}

func TestValidateRejectsUnsafeOrAmbiguousTenants(t *testing.T) {
	tests := []struct {
		name    string
		tenants []Tenant
		want    string
	}{
		{
			name:    "invalid email",
			tenants: []Tenant{{Email: "alice", Name: "alice"}},
			want:    "invalid email",
		},
		{
			name:    "unsafe name",
			tenants: []Tenant{{Email: "alice@example.com", Name: "../alice"}},
			want:    "DNS-safe",
		},
		{
			name: "duplicate name",
			tenants: []Tenant{
				{Email: "alice@example.com", Name: "alice"},
				{Email: "other@example.com", Name: "alice"},
			},
			want: "duplicate tenant name",
		},
		{
			name: "duplicate email",
			tenants: []Tenant{
				{Email: "alice@example.com", Name: "alice"},
				{Email: "alice@example.com", Name: "other"},
			},
			want: "duplicate tenant email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			c.Domain = "code.example.com"
			c.Tenants = tt.tenants
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseRejectsUnknownTopLevelKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	if err := os.WriteFile(path, []byte("domain: code.example.com\nsurprise: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), `unknown key "surprise"`) {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsStrayIndentedLine(t *testing.T) {
	for _, in := range []string{
		"domain: code.example.com\n  stray: true\n",
		"tenants:\n  - email: a@example.com\n    name: a\ntoken_ttl: 1h\n    admin: true\n",
	} {
		path := filepath.Join(t.TempDir(), "tenants.yaml")
		if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(path); err == nil || !strings.Contains(err.Error(), "unexpected indented key") {
			t.Errorf("Parse(%q) error = %v, want unexpected indented key — a mis-indented key must not be silently dropped", in, err)
		}
	}
}

func TestParseStorageSectionSurvivesARoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	input := `domain: code.example.com
storage:
  driver: local
  opt.type: nfs
  opt.o: "addr=10.0.0.9,rw,nfsvers=4"
  opt.device: ":/export/vswarm"
tenants:
  - email: alice@example.com
    name: alice
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Storage.Driver != "local" {
		t.Errorf("driver = %q, want local", got.Storage.Driver)
	}
	for k, want := range map[string]string{
		"type":   "nfs",
		"o":      "addr=10.0.0.9,rw,nfsvers=4",
		"device": ":/export/vswarm",
	} {
		if got.Storage.Opts[k] != want {
			t.Errorf("opt.%s = %q, want %q", k, got.Storage.Opts[k], want)
		}
	}

	got.Path = path
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(path)
	if err != nil {
		t.Fatalf("re-parse after Save() error = %v", err)
	}
	if len(again.Storage.Opts) != len(got.Storage.Opts) {
		t.Fatalf("driver opts lost on round trip: %#v", again.Storage.Opts)
	}
	for k, want := range got.Storage.Opts {
		if again.Storage.Opts[k] != want {
			t.Errorf("after round trip opt.%s = %q, want %q", k, again.Storage.Opts[k], want)
		}
	}
}

func TestParseRejectsUnknownStorageKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	input := `domain: code.example.com
storage:
  drivr: local
tenants:
  - email: alice@example.com
    name: alice
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(path); err == nil {
		t.Fatal("a typo'd storage key must not be silently ignored — it would strand tenant data on the wrong backend")
	}
}

func TestDefaultStorageDriverIsLocal(t *testing.T) {
	c := Default()
	if c.Storage.Driver != "local" {
		t.Errorf("default driver = %q, want local", c.Storage.Driver)
	}
	empty := &Config{Domain: "x.example.com"}
	if err := empty.Validate(); err != nil {
		t.Fatal(err)
	}
	if empty.Storage.Driver != "local" {
		t.Errorf("Validate() should normalise an empty driver to local, got %q", empty.Storage.Driver)
	}
}

func TestTenantReposParseAndExpand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	input := `domain: code.example.com
repo_base: "git@git.example.com:"
tenants:
  - email: alice@example.com
    name: alice
    repos: [Acme/api, Acme/web, https://github.com/other/thing.git]
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tenants[0].Repos) != 3 {
		t.Fatalf("repos = %#v", c.Tenants[0].Repos)
	}
	if got := c.RepoURL("Acme/api"); got != "git@git.example.com:Acme/api" {
		t.Errorf("RepoURL(slug) = %q", got)
	}

	for _, verbatim := range []string{
		"https://github.com/other/thing.git",
		"git@github.com:other/thing.git",
		"ssh://git@host/x/y",
	} {
		if got := c.RepoURL(verbatim); got != verbatim {
			t.Errorf("RepoURL(%q) = %q, want unchanged", verbatim, got)
		}
	}
	for entry, want := range map[string]string{
		"Acme/api":                           "api",
		"https://github.com/other/thing.git": "thing",
		"git@github.com:other/thing.git":     "thing",
	} {
		if got := RepoDir(entry); got != want {
			t.Errorf("RepoDir(%q) = %q, want %q", entry, got, want)
		}
	}

	c.Path = path
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Tenants[0].Repos) != 3 || again.RepoBase != "git@git.example.com:" {
		t.Errorf("repos lost on round trip: base=%q repos=%#v", again.RepoBase, again.Tenants[0].Repos)
	}
}

func TestPlaywrightServiceAcceptedAndImageRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	in := "domain: code.example.com\nplaywright_image: registry.example.com/chrome:1\ntenants:\n" +
		"  - email: a@example.com\n    name: a\n    services: [playwright]\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse() error = %v; playwright must be a known service", err)
	}
	if c.PlaywrightImage != "registry.example.com/chrome:1" {
		t.Fatalf("playwright_image = %q", c.PlaywrightImage)
	}
	c.Path = path
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	rt, err := Parse(path)
	if err != nil || rt.PlaywrightImage != c.PlaywrightImage {
		t.Fatalf("round trip mismatch: %v %#v", err, rt)
	}
	if Default().PlaywrightImage == "" {
		t.Fatal("default playwright_image must not be empty")
	}
}

func TestParseRejectsUnknownTenantKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	in := "domain: code.example.com\ntenants:\n  - email: a@example.com\n    name: a\n    repo: [Acme/api]\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), `unknown tenant key "repo"`) {
		t.Fatalf("Parse() error = %v, want unknown tenant key", err)
	}
}

func TestParseRejectsUnknownResourcesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	in := "domain: code.example.com\nresources:\n  cpu: \"2\"\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), `unknown resources key "cpu"`) {
		t.Fatalf("Parse() error = %v, want unknown resources key", err)
	}
}

func TestParseRejectsInvalidPids(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	in := "domain: code.example.com\nresources:\n  pids: lots\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), `invalid pids "lots"`) {
		t.Fatalf("Parse() error = %v, want invalid pids", err)
	}
}

func TestValidateRejectsQuoteBreakingEmails(t *testing.T) {
	c := Default()
	c.Domain = "code.example.com"
	c.Tenants = []Tenant{{Email: "a# b@example.com", Name: "a"}}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() accepted an email that breaks the saved format")
	}
}

func TestSaveQuotesEmailsSoTheyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	c := Default()
	c.Domain = "code.example.com"
	c.Path = path
	c.Tenants = []Tenant{{Email: "space man@example.com", Name: "spaceman"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse(saved config) error = %v", err)
	}
	if len(roundTrip.Tenants) != 1 || roundTrip.Tenants[0].Email != "space man@example.com" {
		t.Fatalf("email did not survive the round trip: %#v", roundTrip.Tenants)
	}
}

func TestParseMountsSurviveARoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	input := `domain: code.example.com
mounts:
  - /opt/dislace/vswarm/cli:/opt/dislace-cli
tenants:
  - email: alice@example.com
    name: alice
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(got.Mounts) != 1 || got.Mounts[0].Source != "/opt/dislace/vswarm/cli" ||
		got.Mounts[0].Target != "/opt/dislace-cli" {
		t.Fatalf("unexpected mounts: %#v", got.Mounts)
	}

	got.Path = path
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(path)
	if err != nil {
		t.Fatalf("re-parse after Save() error = %v", err)
	}
	if len(again.Mounts) != 1 || again.Mounts[0] != got.Mounts[0] {
		t.Fatalf("mounts lost on round trip: %#v", again.Mounts)
	}
}

func TestValidateRejectsUnsafeMounts(t *testing.T) {
	cases := map[string][]Mount{
		"relative source":         {{Source: "cli", Target: "/opt/dislace-cli"}},
		"relative target":         {{Source: "/opt/cli", Target: "opt/dislace-cli"}},
		"traversal":               {{Source: "/opt/../etc", Target: "/opt/dislace-cli"}},
		"traversal spelt with .":  {{Source: "/opt/cli", Target: "/home/./ai-agent"}},
		"traversal spelt with //": {{Source: "/opt/cli", Target: "//home/ai-agent"}},
		"trailing slash":          {{Source: "/opt/cli", Target: "/opt/dislace-cli/"}},
		"over the tenant home":    {{Source: "/opt/cli", Target: "/home/ai-agent/.config"}},
		"over the tooling manifest": {
			{Source: "/opt/cli", Target: "/etc/vswarm-tooling/tools.tsv"},
		},
		"over the run tmpfs": {{Source: "/opt/cli", Target: "/run"}},
		"shadowing a reserved parent": {
			{Source: "/opt/cli", Target: "/etc/vswarm-tooling"},
		},
		"compose interpolation": {{Source: "/opt/${SECRET}/cli", Target: "/opt/dislace-cli"}},
		"duplicate target": {
			{Source: "/opt/cli", Target: "/opt/dislace-cli"},
			{Source: "/opt/other", Target: "/opt/dislace-cli"},
		},
		"duplicate target spelt with .": {
			{Source: "/opt/cli", Target: "/opt/dislace-cli"},
			{Source: "/opt/other", Target: "/opt/./dislace-cli"},
		},
	}
	for name, mounts := range cases {
		c := Default()
		c.Domain = "code.example.com"
		c.Mounts = mounts
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted %#v", name, mounts)
		}
	}
}

func TestValidateAcceptsACanonicalDottedPath(t *testing.T) {
	c := Default()
	c.Domain = "code.example.com"
	c.Mounts = []Mount{{Source: "/opt/my..dir", Target: "/opt/dislace-cli"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() rejected a canonical path: %v", err)
	}
}

func TestParseRejectsMountWithoutATarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	input := `domain: code.example.com
mounts:
  - /opt/dislace/vswarm/cli
tenants:
  - email: alice@example.com
    name: alice
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(path); err == nil || !strings.Contains(err.Error(), "source:target") {
		t.Fatalf("Parse() error = %v, want a source:target complaint", err)
	}
}

func parseRoster(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Parse(path)
	if err != nil {
		return nil, err
	}
	return c, c.Validate()
}

func TestNetIDIsOptionalAndChecked(t *testing.T) {
	base := "domain: example.com\ntenants:\n  - email: a@example.com\n    name: a\n"

	t.Run("absent leaves it zero so render falls back to position", func(t *testing.T) {
		c, err := parseRoster(t, base)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.Tenants[0].NetID != 0 {
			t.Fatalf("net_id = %d, want 0 when the roster omits it", c.Tenants[0].NetID)
		}
	})

	t.Run("declared is carried through", func(t *testing.T) {
		c, err := parseRoster(t, base+"    net_id: 17\n")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.Tenants[0].NetID != 17 {
			t.Fatalf("net_id = %d, want 17", c.Tenants[0].NetID)
		}
	})

	t.Run("out of range is refused", func(t *testing.T) {
		if _, err := parseRoster(t, base+"    net_id: 9\n"); err == nil {
			t.Fatal("net_id 9 collides with the edge network and must be refused")
		}
		if _, err := parseRoster(t, base+"    net_id: 255\n"); err == nil {
			t.Fatal("net_id 255 is the broadcast address and must be refused")
		}
	})

	t.Run("two tenants cannot share one subnet", func(t *testing.T) {
		roster := base + "    net_id: 11\n  - email: b@example.com\n    name: b\n    net_id: 11\n"
		if _, err := parseRoster(t, roster); err == nil {
			t.Fatal("a shared net_id authorizes one tenant's keys on another's subnet")
		}
	})
}
