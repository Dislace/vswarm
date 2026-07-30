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

func volumeRun(image, entrypoint string, mounts, args []string) error {
	return dockerx.Run("docker", volumeRunArgs(image, entrypoint, mounts, args)...)
}

func volumeRunArgs(image, entrypoint string, mounts, args []string) []string {
	full := []string{"run", "--rm", "-u", "0", "--network", "none", "--entrypoint", entrypoint}
	full = append(full, mounts...)
	full = append(full, image)
	return append(full, args...)
}

func revoke(image, volume string, paths []string) error {
	args := []string{"-rf"}
	for _, p := range paths {
		args = append(args, "/dst/"+p)
	}
	return volumeRun(image, "rm", []string{"-v", volume + ":/dst"}, args)
}

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

func deliver(image, stage, volume string) error {
	return volumeRun(image, "bash",
		[]string{"-v", stage + ":/src:ro", "-v", volume + ":/dst"},
		[]string{"-c", deliverScript})
}

const deliverScript = `set -euo pipefail
cp -a /src/. /dst/
chown 1000:1000 /dst
chmod 0755 /dst
find /src -mindepth 1 -maxdepth 1 -printf '%P\0' | while IFS= read -r -d '' e; do
  chown -R 1000:1000 "/dst/$e"
done
if [ -d /dst/.ssh ]; then
  chmod 0700 /dst/.ssh
  find /dst/.ssh -maxdepth 1 -type f -exec chmod 0600 {} +
fi
find /dst -maxdepth 1 -type f -name '*.env' -exec chmod 0600 {} +
`

func copyTree(image, src, dst string) error {
	return volumeRun(image, "sh",
		[]string{"-v", src + ":/src:ro", "-v", dst + ":/dst"},
		[]string{"-c", "set -eu; cp -a /src/. /dst/"})
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
	if err := volumeRun(c.Image, "bash",
		[]string{"-v", abs + ":/src:ro", "-v", vol + ":/dst"},
		[]string{"-c", migrateScript(excludes)}); err != nil {
		return fmt.Errorf("migrate %s: %w", name, err)
	}

	fmt.Printf("migrated %s -> %s\n", legacy, vol)
	if !keepDerived {
		fmt.Printf("  dropped: %s\n", strings.Join(derivedPaths, " "))
	}
	fmt.Printf("  %s left in place — verify the workspace, then remove it yourself\n", legacy)
	return nil
}

func migrateScript(excludes []string) string {
	return "set -euo pipefail; tar -C /src -cf - " + strings.Join(excludes, " ") +
		" . | tar -C /dst -xf -; chown -R 1000:1000 /dst"
}

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
