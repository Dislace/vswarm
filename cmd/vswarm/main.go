package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/dockerx"
	"github.com/dislace/vswarm/internal/render"
)

const (
	tenantsFile    = "tenants.yaml"
	proxyContainer = "vswarm-proxy"
	t3BaseDir      = "/home/ai-agent/.config/t3"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit()
	case "render":
		err = cmdRender()
	case "up":
		err = cmdUp()
	case "down":
		err = cmdDown()
	case "build":
		err = cmdBuild()
	case "status":
		err = cmdStatus()
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "doctor":
		err = cmdDoctor()
	case "tenant":
		err = cmdTenant(os.Args[2:])
	case "pair":
		err = cmdPair(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`vswarm — VibeSwarm operator CLI

USAGE
  vswarm <command> [args]

COMMANDS
  init                     scaffold tenants.yaml, .env, config/ (idempotent)
  render                   tenants.yaml -> generated/ (compose + angie + image)
  build                    build the workspace image from generated/image
  up                       render, start the stack, provision every tenant token
  down                     stop the stack
  tenant add <email> <name>   add a tenant; start + pair it   (--no-up to skip)
  tenant rm <name>            remove a tenant                  (--purge to wipe data)
  tenant ls                   list tenants + container status
  pair <name>              (re)mint a tenant's T3 token and inject it into angie
  status                   docker compose ps
  logs [tenant]            follow logs (proxy by default)
  doctor                   verify isolation + config invariants
`)
}

func loadConfig() (*config.Config, error) {
	c, err := config.Parse(tenantsFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", tenantsFile, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func cmdInit() error {
	if _, err := os.Stat(tenantsFile); os.IsNotExist(err) {
		if err := os.WriteFile(tenantsFile, []byte(defaultTenants), 0o644); err != nil {
			return err
		}
		fmt.Println("created", tenantsFile)
	} else {
		fmt.Println(tenantsFile, "already exists — left unchanged")
	}
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		if err := os.WriteFile(".env", []byte(defaultEnv), 0o600); err != nil {
			return err
		}
		fmt.Println("created .env")
	}
	if err := os.MkdirAll("config", 0o755); err != nil {
		return err
	}
	fmt.Println("next: edit tenants.yaml + .env, then `vswarm build && vswarm up`")
	return nil
}

func cmdRender() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if err := render.Render(c); err != nil {
		return err
	}
	fmt.Println("rendered -> generated/")
	return nil
}

func cmdUp() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if err := render.Render(c); err != nil {
		return err
	}
	if err := dockerx.Compose("up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	for _, t := range c.Tenants {
		if err := pair(c, t.Name); err != nil {
			return fmt.Errorf("pair %s: %w", t.Name, err)
		}
	}
	fmt.Println("up: stack running, all tenants provisioned")
	return nil
}

func cmdDown() error { return dockerx.Compose("down") }

func cmdBuild() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if err := render.Render(c); err != nil {
		return err
	}
	return dockerx.Run("docker", "build", "-t", c.Image, "generated/image")
}

func cmdStatus() error { return dockerx.Compose("ps") }

func cmdLogs(args []string) error {
	svc := proxyContainer
	if len(args) > 0 {
		svc = "vswarm-" + args[0]
	}
	return dockerx.Compose("logs", "-f", svc)
}

func cmdPair(args []string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: vswarm pair <name>")
	}
	return pair(c, args[0])
}

func pair(c *config.Config, name string) error {
	t, ok := c.Tenant(name)
	if !ok {
		return fmt.Errorf("no such tenant %q", name)
	}
	container := "vswarm-" + name
	if err := waitHealthy(container, 150*time.Second); err != nil {
		return err
	}
	out, err := dockerx.Exec(container, "t3", "auth", "session", "issue",
		"--base-dir", t3BaseDir, "--ttl", c.TokenTTL, "--json")
	if err != nil {
		return err
	}
	token, err := extractToken(out)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%q %q;\n", t.Email, token)
	p := filepath.Join(render.GeneratedDir, "angie", "tenants", name+".token")
	if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
		return err
	}
	if _, err := dockerx.Exec(proxyContainer, "angie", "-s", "reload"); err != nil {
		return err
	}
	fmt.Printf("paired %s (token injected, proxy reloaded)\n", name)
	return nil
}

func waitHealthy(container string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, err := dockerx.Output("docker", "inspect", "-f", "{{.State.Health.Status}}", container)
		if err == nil && strings.TrimSpace(out) == "healthy" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not become healthy within %s", container, timeout)
		}
		time.Sleep(3 * time.Second)
	}
}

func extractToken(s string) (string, error) {
	var r struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &r); err != nil {
		return "", fmt.Errorf("parse token json: %w (output: %s)", err, truncate(s, 200))
	}
	if r.Token == "" {
		return "", fmt.Errorf("empty token in t3 response")
	}
	return r.Token, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

const defaultTenants = `# VibeSwarm tenant manifest — the only file you edit by hand.
domain: t3code.example.com
image: vswarm/workspace:latest
resources:
  cpus: "2.0"
  memory: 6g
  pids: 512
token_ttl: 30d
tenants:
  - email: you@example.com
    name: you
`

const defaultEnv = `# Cloudflare Tunnel token (Zero Trust dashboard). Required.
VSWARM_TUNNEL_TOKEN=
# Optional image registry prefix (e.g. ghcr.io/dislace).
VSWARM_REGISTRY=
COMPOSE_PROJECT_NAME=vswarm
`
