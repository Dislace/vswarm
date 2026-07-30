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
