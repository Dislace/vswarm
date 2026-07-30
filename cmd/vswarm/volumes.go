package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/dockerx"
	"github.com/dislace/vswarm/internal/render"
)

// Paths under the tenant home that any machine with a network can rebuild.
// `vswarm migrate` drops them rather than hauling them to the new volume;
// they are typically the majority of a home's size.
var derivedPaths = []string{
	".cache",
	".npm",
	".bun/install/cache",
	".vite-plus",
	"go/pkg",
	"node_modules",
}

func cmdProvision(args []string) error {
	from := ""
	var remove, pos []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--from" && i+1 < len(args):
			from = args[i+1]
			i++
		case args[i] == "--remove" && i+1 < len(args):
			remove = append(remove, args[i+1])
			i++
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) < 1 {
		return fmt.Errorf("usage: vswarm provision <name> [--from <dir>] [--remove <rel-path>]...")
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	return provisionTenant(c, pos[0], from, remove...)
}

// provisionTenant delivers everything vswarm owns the delivery of into a
// tenant's work volume: the postgres contract it mints itself, plus whatever
// tree the deployment layer staged. Callable with an empty `from` — `up` does
// exactly that so a fresh workspace gets its ~/.pg.env without an extra step.
//
// `remove` paths are deleted from the volume. Delivery alone cannot express
// revocation, and revocation is the half that matters: a tenant that loses
// `admin: true` must lose the key with it, not keep it until someone reads a
// doctor failure.
func provisionTenant(c *config.Config, name, from string, remove ...string) error {
	t, ok := c.Tenant(name)
	if !ok {
		return fmt.Errorf("no such tenant %q", name)
	}
	for _, r := range remove {
		if err := checkRelPath(r); err != nil {
			return err
		}
	}

	stage, err := os.MkdirTemp("", "vswarm-provision-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	if from != "" {
		abs, err := filepath.Abs(from)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("--from %q: %w", from, err)
		}
		if err := copyTree(c.Image, abs, stage); err != nil {
			return fmt.Errorf("stage %s: %w", from, err)
		}
	}

	if len(t.Repos) > 0 {
		dir := filepath.Join(stage, ".config", "vswarm")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		var manifest strings.Builder
		manifest.WriteString("# Delivered by `vswarm provision` from tenants.yaml. Run `vswarm-repos sync`.\n")
		for _, r := range t.Repos {
			manifest.WriteString(c.RepoURL(r) + "\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "repos"), []byte(manifest.String()), 0o644); err != nil {
			return err
		}
	}

	if t.HasService("postgres") {
		pw, err := os.ReadFile(render.PGPasswordPath(name))
		if err != nil {
			return fmt.Errorf("read pg password (run `vswarm render` first): %w", err)
		}
		env := render.PGEnv("vswarm-db-"+name, "postgres", "postgres", strings.TrimSpace(string(pw)))
		if err := os.WriteFile(filepath.Join(stage, ".pg.env"), []byte(env), 0o600); err != nil {
			return err
		}
	}

	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	if len(entries) == 0 && len(remove) == 0 {
		return nil
	}

	vol := render.WorkVolume(name)
	if err := requireVolume(vol); err != nil {
		return err
	}
	if len(entries) > 0 {
		if err := deliver(c.Image, stage, vol); err != nil {
			return err
		}
	}
	if len(remove) > 0 {
		if err := revoke(c.Image, vol, remove); err != nil {
			return err
		}
	}
	fmt.Printf("provisioned %s (%d delivered, %d removed -> %s)\n", name, len(entries), len(remove), vol)
	return nil
}

// revoke deletes paths from the work volume. Idempotent: a path that is
// already gone is the desired state, not an error.
func revoke(image, volume string, paths []string) error {
	args := []string{"run", "--rm", "-u", "0", "--network", "none", "-v", volume + ":/dst", image, "rm", "-rf"}
	for _, p := range paths {
		args = append(args, "/dst/"+p)
	}
	return dockerx.Run("docker", args...)
}

// checkRelPath refuses anything that could escape the volume. These paths are
// interpolated into an `rm -rf` running as root, so "the caller is Ansible" is
// not a good enough reason to skip the check.
func checkRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("--remove: empty path")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("--remove %q: must be relative to the tenant home", p)
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("--remove %q: escapes the tenant home", p)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return fmt.Errorf("--remove %q: escapes the tenant home", p)
		}
	}
	return nil
}

// deliver copies a staged tree into the tenant's work volume through a
// throwaway root container. Going through the container is what lets the
// volume live behind any driver: vswarm never needs to know where the data
// actually sits on (or off) this host.
//
// Modes are applied here rather than trusted from the staging tree so the
// delivered contract is the same whatever the deployment layer handed us.
func deliver(image, stage, volume string) error {
	const script = `set -eu
cp -a /src/. /dst/
find /src -mindepth 1 -maxdepth 1 -printf '%P\0' | while IFS= read -r -d '' e; do
  chown -R 1000:1000 "/dst/$e"
done
if [ -d /dst/.ssh ]; then
  chmod 0700 /dst/.ssh
  find /dst/.ssh -maxdepth 1 -type f -exec chmod 0600 {} +
fi
find /dst -maxdepth 1 -type f -name '*.env' -exec chmod 0600 {} +
`
	return dockerx.Run("docker", "run", "--rm", "-u", "0",
		"--network", "none",
		"-v", stage+":/src:ro",
		"-v", volume+":/dst",
		image, "sh", "-c", script)
}

// copyTree stages a host directory with a container so ownership in the
// staging area never depends on who ran vswarm.
func copyTree(image, src, dst string) error {
	return dockerx.Run("docker", "run", "--rm", "-u", "0",
		"--network", "none",
		"-v", src+":/src:ro",
		"-v", dst+":/dst",
		image, "sh", "-c", "set -eu; cp -a /src/. /dst/")
}

func cmdMigrate(args []string) error {
	pos, keepDerived := takeFlag(args, "--keep-derived")
	if len(pos) < 1 {
		return fmt.Errorf("usage: vswarm migrate <name> [--keep-derived]")
	}
	name := pos[0]

	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := c.Tenant(name); !ok {
		return fmt.Errorf("no such tenant %q", name)
	}

	container := "vswarm-" + name
	if running(container) {
		return fmt.Errorf("%s is running — stop it first (`docker compose -f generated/docker-compose.yml stop %s`)", container, container)
	}

	legacy := filepath.Join("config", name, "home")
	info, err := os.Stat(legacy)
	if err != nil {
		return fmt.Errorf("no legacy home at %s: %w", legacy, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", legacy)
	}
	abs, err := filepath.Abs(legacy)
	if err != nil {
		return err
	}

	// Carry the postgres password across before anything else: it used to live
	// in the home as ~/.pg.env and is the one value a re-render cannot re-mint
	// without silently locking the tenant out of its own database.
	if err := adoptPGPassword(name, filepath.Join(legacy, ".pg.env")); err != nil {
		return err
	}

	vol := render.WorkVolume(name)
	if err := requireVolume(vol); err != nil {
		return err
	}

	var excludes []string
	if !keepDerived {
		for _, p := range derivedPaths {
			excludes = append(excludes, "--exclude="+p)
		}
	}
	if err := dockerx.Run("docker", "run", "--rm", "-u", "0",
		"--network", "none",
		"-v", abs+":/src:ro",
		"-v", vol+":/dst",
		c.Image, "bash", "-c", migrateScript(excludes)); err != nil {
		return fmt.Errorf("migrate %s: %w", name, err)
	}

	fmt.Printf("migrated %s -> %s\n", legacy, vol)
	if !keepDerived {
		fmt.Printf("  dropped: %s\n", strings.Join(derivedPaths, " "))
	}
	fmt.Printf("  %s left in place — verify the workspace, then remove it yourself\n", legacy)
	return nil
}

// migrateScript copies /src into /dst through a tar pipe.
//
// Run under bash with pipefail, not sh: under sh the pipeline's status is the
// extracting tar, which exits 0 after unpacking a truncated stream, so a
// failing source tar would be reported as a successful migration. The legacy
// home is the only other copy of this data, and the operator is told to delete
// it once the workspace looks right — a silent partial copy is how that
// deletion loses work.
func migrateScript(excludes []string) string {
	return "set -euo pipefail; tar -C /src -cf - " + strings.Join(excludes, " ") +
		" . | tar -C /dst -xf -; chown -R 1000:1000 /dst"
}

// adoptPGPassword lifts the password out of a legacy ~/.pg.env into the
// host-side store render now reads. Never overwrites an existing value.
func adoptPGPassword(name, legacyEnv string) error {
	dst := render.PGPasswordPath(name)
	if raw, err := os.ReadFile(dst); err == nil && strings.TrimSpace(string(raw)) != "" {
		return nil
	}
	raw, err := os.ReadFile(legacyEnv)
	if err != nil {
		return nil
	}
	pw := envValue(string(raw), "PGPASSWORD")
	if pw == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, []byte(pw+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("  adopted postgres password from %s\n", legacyEnv)
	return os.Chmod(dst, 0o600)
}

func envValue(env, key string) string {
	for _, ln := range strings.Split(env, "\n") {
		if k, v, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// requireVolume refuses to let `docker run` auto-create a missing volume: it
// would be created with the default driver, quietly ignoring `storage:` and
// stranding the tenant's data on the wrong backend.
func requireVolume(name string) error {
	if _, err := dockerx.Output("docker", "volume", "inspect", name); err != nil {
		return fmt.Errorf("volume %s does not exist — run `vswarm up` first so it is created with the configured driver", name)
	}
	return nil
}

func running(container string) bool {
	out, err := dockerx.Output("docker", "inspect", "-f", "{{.State.Running}}", container)
	return err == nil && strings.TrimSpace(out) == "true"
}
