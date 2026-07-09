package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dislace/vswarm/internal/config"
)

func TestRenderProducesIsolatedTenantConfiguration(t *testing.T) {
	chdirTemp(t)
	c := &config.Config{
		Domain:       "code.example.com",
		Image:        "registry.example.com/vswarm:v1",
		Team:         "platform",
		Resources:    config.Resources{CPUs: "2.5", Memory: "7g", Pids: 3072},
		ManageTunnel: true,
		Tenants: []config.Tenant{
			{Email: "alice@example.com", Name: "alice"},
			{Email: "bob@example.com", Name: "bob"},
		},
	}

	if err := Render(c); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	compose := readFile(t, filepath.Join(GeneratedDir, "docker-compose.yml"))
	for _, want := range []string{
		"subnet: 172.31.10.0/24",
		"subnet: 172.31.11.0/24",
		"vswarm-alice:",
		"vswarm-bob:",
		"image: registry.example.com/vswarm:v1",
		"hostname: platform",
		`cpus: "2.5"`,
		"memory: 7g",
		"pids_limit: 3072",
		"vswarm-tunnel:",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("generated compose missing %q", want)
		}
	}

	assertFileEquals(
		t,
		filepath.Join(GeneratedDir, "angie", "tenants", "alice.upstream"),
		"\"alice@example.com\" \"vswarm-alice:3773\";\n",
	)
	assertFileEquals(
		t,
		filepath.Join(GeneratedDir, "angie", "tenants", "bob.upstream"),
		"\"bob@example.com\" \"vswarm-bob:3773\";\n",
	)

	entrypoint := filepath.Join(GeneratedDir, "image", "entrypoint.sh")
	info, err := os.Stat(entrypoint)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("entrypoint mode = %o, want 755", got)
	}
}

func TestRenderRemovesDepartedTenantRoutingAndToken(t *testing.T) {
	chdirTemp(t)
	c := &config.Config{
		Domain:    "code.example.com",
		Image:     "vswarm/workspace:test",
		Resources: config.Resources{CPUs: "1", Memory: "1g", Pids: 128},
		Tenants:   []config.Tenant{{Email: "alice@example.com", Name: "alice"}},
	}
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(GeneratedDir, "angie", "tenants", "alice.token")
	if err := os.WriteFile(token, []byte("\"alice@example.com\" \"secret\";\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c.Tenants = nil
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(GeneratedDir, "angie", "tenants", "alice.upstream"),
		token,
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists after tenant removal", path)
		}
	}
}

func chdirTemp(t *testing.T) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertFileEquals(t *testing.T, path, want string) {
	t.Helper()
	if got := readFile(t, path); got != want {
		t.Errorf("%s:\nwant %q\ngot  %q", path, want, got)
	}
}
