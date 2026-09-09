package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/dockerx"
	"github.com/dislace/vswarm/internal/render"
)

const (
	tenantsFile    = "tenants.yaml"
	proxyContainer = "vswarm-proxy"
	t3BaseDir      = "/home/ai-agent/.config/t3"

	sessionIssueAttempts = 5
	sessionIssueBackoff  = 2 * time.Second
)

// version is stamped by the release build. A deployment installs a pinned
// binary and needs to tell what it already has without fetching it again.
var version = "dev"

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
		err = cmdUp(os.Args[2:])
	case "down":
		err = cmdDown()
	case "build":
		err = cmdBuild()
	case "status":
		err = cmdStatus(os.Args[2:])
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "tenant":
		err = cmdTenant(os.Args[2:])
	case "pair":
		err = cmdPair(os.Args[2:])
	case "provision":
		err = cmdProvision(os.Args[2:])
	case "migrate":
		err = cmdMigrate(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
		return
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
  render                   tenants.yaml -> generated/ (compose + angie)
  build                    build ./image and tag it with the image: from
                           tenants.yaml (vswarm checkout only; hosts pull)
  up                       render, start the stack, provision + pair every tenant
                           (--json reports what each container did: created,
                            recreated, unchanged or absent)
  down                     stop the stack
  tenant add <email> <name>   add a tenant; start + pair it   (--no-up to skip)
  tenant rm <name>            remove a tenant                  (--purge to wipe data)
  tenant ls                   list tenants + container status
  pair <name>              reconcile a tenant to one T3 session: reuse it while it
                           has life left, mint when it does not, revoke the rest,
                           inject it into angie and deliver it to the workspace
  provision <name>         make a tenant's work volume match a staging tree
                           (--from <dir> is the desired state: what it holds is
                            delivered, what vswarm delivered before and it no
                            longer holds is taken back, and nothing the tenant
                            made is touched. --remove <rel-path> is an escape
                            hatch for paths vswarm never delivered, repeatable)
  migrate <name>           copy a legacy config/<name>/home bind mount into the
                           work volume, dropping rebuildable caches
                           (--keep-derived copies them too)
  status                   docker compose ps                       (--json)
  logs [tenant]            follow logs (proxy by default)
  doctor                   verify isolation + config invariants
                           (--wait=30s retries until they pass or time out)
  version                  print the release this binary was built from
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
	fmt.Println("next: edit tenants.yaml + .env (set `image:` to a published tag), then `vswarm up`")
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

func cmdUp(args []string) error {
	_, asJSON := takeJSON(args)
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if err := render.Render(c); err != nil {
		return err
	}
	before := containerIDs()
	if err := dockerx.ComposeTo(humanOut, "up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	report := classifyUp(stackContainers(c), before, containerIDs())
	if err := runParallel(len(c.Tenants), func(i int) error {
		t := c.Tenants[i]
		if err := provisionTenant(c, t.Name, ""); err != nil {
			return fmt.Errorf("provision %s: %w", t.Name, err)
		}
		if err := pairMint(c, t.Name); err != nil {
			return fmt.Errorf("pair %s: %w", t.Name, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(c.Tenants) > 0 {
		if err := reloadProxy(); err != nil {
			return err
		}
	}
	fmt.Fprintln(humanOut, "up: stack running, all tenants provisioned")
	if asJSON {
		return emitJSON(report)
	}
	return nil
}

func cmdDown() error { return dockerx.Compose("down") }

// imageContext is the committed build context. The image is an input to a
// deployment, not something a deployment renders: CI builds this directory and
// publishes the result, and a host names the published tag in `image:`.
const imageContext = "image"

func cmdBuild() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(imageContext, "Dockerfile")); err != nil {
		return fmt.Errorf("no build context at ./%s — `build` runs from a vswarm checkout; "+
			"a deployment pulls the published image named by `image:`", imageContext)
	}
	return dockerx.Run("docker", "build", "-t", c.Image, imageContext)
}

func cmdStatus(args []string) error {
	if _, asJSON := takeJSON(args); asJSON {
		c, err := loadConfig()
		if err != nil {
			return err
		}
		return emitJSON(stackStatus(c))
	}
	return dockerx.Compose("ps")
}

func cmdLogs(args []string) error {
	svc := proxyContainer
	if len(args) > 0 {
		svc = "vswarm-" + args[0]
	}
	return dockerx.Compose("logs", "-f", svc)
}

const defaultTenants = `# VibeSwarm tenant manifest — the only file you edit by hand.
domain: t3code.example.com
image: ghcr.io/dislace/vswarm-workspace:latest
resources:
  cpus: "2.0"
  memory: 6g
  pids: 4096
token_ttl: 30d
tenants:
  - email: you@example.com
    name: you
`

const defaultEnv = `# Cloudflare Tunnel token (Zero Trust dashboard). Required.
VSWARM_TUNNEL_TOKEN=
COMPOSE_PROJECT_NAME=vswarm
`
