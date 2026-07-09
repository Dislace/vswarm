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
		roundTrip.Team != got.Team ||
		roundTrip.Resources != got.Resources ||
		roundTrip.TokenTTL != got.TokenTTL ||
		roundTrip.ManageTunnel != got.ManageTunnel ||
		roundTrip.EdgeExternal != got.EdgeExternal ||
		len(roundTrip.Tenants) != len(got.Tenants) {
		t.Fatalf("round trip mismatch:\nwant %#v\ngot  %#v", got, roundTrip)
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
