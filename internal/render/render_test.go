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

	tooling := filepath.Join(GeneratedDir, "image", "vswarm-tooling")
	info, err = os.Stat(tooling)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("vswarm-tooling mode = %o, want 755", got)
	}
	reconcile := filepath.Join(GeneratedDir, "image", "vswarm-codex-state-reconcile")
	info, err = os.Stat(reconcile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("vswarm-codex-state-reconcile mode = %o, want 755", got)
	}
	manifest := readFile(t, filepath.Join(GeneratedDir, "image", "tools.tsv"))
	for _, want := range []string{
		"claude|npm|@anthropic-ai/claude-code|claude|",
		"codex|npm|@openai/codex|codex|",
		"bun|npm|bun|bun|",
		"go|go|go.dev|go|",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("generated tooling manifest missing %q", want)
		}
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

func TestRenderBacksTenantHomeWithVolumesNotABindMount(t *testing.T) {
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
	compose := readFile(t, filepath.Join(GeneratedDir, "docker-compose.yml"))

	if strings.Contains(compose, "./config/alice/home") {
		t.Error("compose still bind-mounts the tenant home; the whole point is that it does not")
	}
	for _, want := range []string{
		"- vswarm-work-alice:/home/ai-agent",
		"- vswarm-cache-alice:/home/ai-agent/.cache",
		"  vswarm-work-alice:\n    name: vswarm-work-alice",
		"  vswarm-cache-alice:\n    name: vswarm-cache-alice",
		"- XDG_CACHE_HOME=/home/ai-agent/.cache",
		"- GOMODCACHE=/home/ai-agent/.cache/go/mod",
		"- npm_config_cache=/home/ai-agent/.cache/npm",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("generated compose missing %q", want)
		}
	}
}

func TestRenderAppliesStorageDriverToDurableVolumesOnly(t *testing.T) {
	chdirTemp(t)
	c := &config.Config{
		Domain:    "code.example.com",
		Image:     "vswarm/workspace:test",
		Resources: config.Resources{CPUs: "1", Memory: "1g", Pids: 128},
		Storage: config.Storage{Driver: "local", Opts: map[string]string{
			"type":   "nfs",
			"o":      "addr=10.0.0.9,rw",
			"device": ":/export/vswarm",
		}},
		Tenants: []config.Tenant{{Email: "alice@example.com", Name: "alice", Services: []string{"postgres"}}},
	}
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	compose := readFile(t, filepath.Join(GeneratedDir, "docker-compose.yml"))

	work := section(compose, "  vswarm-work-alice:")
	for _, want := range []string{`device: ":/export/vswarm"`, `o: "addr=10.0.0.9,rw"`, `type: "nfs"`} {
		if !strings.Contains(work, want) {
			t.Errorf("work volume missing driver opt %q, got:\n%s", want, work)
		}
	}
	if db := section(compose, "  vswarm-dbdata-alice:"); !strings.Contains(db, `type: "nfs"`) {
		t.Errorf("db volume should carry the driver opts too, got:\n%s", db)
	}

	if cache := section(compose, "  vswarm-cache-alice:"); strings.Contains(cache, "driver_opts") {
		t.Errorf("cache volume must not inherit the remote driver, got:\n%s", cache)
	}
}

func TestResolvePGPasswordPersistsOutsideTheTenantHome(t *testing.T) {
	chdirTemp(t)
	c := &config.Config{
		Domain:    "code.example.com",
		Image:     "vswarm/workspace:test",
		Resources: config.Resources{CPUs: "1", Memory: "1g", Pids: 128},
		Tenants:   []config.Tenant{{Email: "alice@example.com", Name: "alice", Services: []string{"postgres"}}},
	}
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	first := strings.TrimSpace(readFile(t, PGPasswordPath("alice")))
	if first == "" {
		t.Fatal("no postgres password minted")
	}
	info, err := os.Stat(PGPasswordPath("alice"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("pg.password mode = %o, want 600", got)
	}

	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	if second := strings.TrimSpace(readFile(t, PGPasswordPath("alice"))); second != first {
		t.Errorf("re-render rotated the postgres password: %q -> %q", first, second)
	}
	if !strings.Contains(readFile(t, filepath.Join(GeneratedDir, "docker-compose.yml")), "POSTGRES_PASSWORD="+first) {
		t.Error("compose does not carry the persisted password")
	}

	if _, err := os.Stat(filepath.Join("config", "alice", "home")); !os.IsNotExist(err) {
		t.Errorf("render wrote into the tenant home: %v", err)
	}
}

func section(doc, header string) string {
	i := strings.Index(doc, header)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(header):]
	indent := len(header) - len(strings.TrimLeft(header, " "))
	var out []string
	for _, ln := range strings.Split(rest, "\n") {
		if strings.TrimSpace(ln) != "" && len(ln)-len(strings.TrimLeft(ln, " ")) <= indent {
			break
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
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

func TestImageTemplatesAgreeWithTheCacheConstant(t *testing.T) {
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
	for _, f := range []string{"Dockerfile", "entrypoint.sh"} {
		body := readFile(t, filepath.Join(GeneratedDir, "image", f))
		for _, sub := range []string{"npm", "bun", "go/mod", "go/build", "pip"} {
			if !strings.Contains(body, CacheDir+"/"+sub) {
				t.Errorf("%s does not create %s/%s", f, CacheDir, sub)
			}
		}
	}
}
