package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dislace/vibeswarm/internal/config"
	"github.com/dislace/vibeswarm/internal/dockerx"
	"github.com/dislace/vibeswarm/internal/render"
)

func takeFlag(args []string, names ...string) ([]string, bool) {
	set := false
	var pos []string
	for _, a := range args {
		match := false
		for _, n := range names {
			if a == n {
				match = true
				break
			}
		}
		if match {
			set = true
			continue
		}
		pos = append(pos, a)
	}
	return pos, set
}

func cmdTenant(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vswarm tenant <add|rm|ls> ...")
	}
	switch args[0] {
	case "add":
		return tenantAdd(args[1:])
	case "rm", "remove":
		return tenantRm(args[1:])
	case "ls", "list":
		return tenantLs()
	default:
		return fmt.Errorf("unknown tenant subcommand %q", args[0])
	}
}

func tenantAdd(args []string) error {
	pos, noUp := takeFlag(args, "--no-up", "-no-up")
	if len(pos) < 2 {
		return fmt.Errorf("usage: vswarm tenant add <email> <name> [--no-up]")
	}
	email, name := pos[0], pos[1]

	c, err := config.Parse(tenantsFile)
	if err != nil {
		return err
	}
	if err := c.AddTenant(email, name); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	if err := scaffoldTenant(name); err != nil {
		return err
	}
	if err := render.Render(c); err != nil {
		return err
	}
	if noUp {
		fmt.Printf("added %s (%s); run `vswarm up` to start it\n", name, email)
		return nil
	}
	if err := dockerx.Compose("up", "-d", "vswarm-"+name); err != nil {
		return err
	}
	return pair(c, name)
}

func scaffoldTenant(name string) error {
	base := filepath.Join("config", name, "home")
	for _, sub := range []string{"repos", ".config/t3"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return err
		}
	}
	ssh := filepath.Join(base, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		return err
	}
	return os.Chmod(ssh, 0o700)
}

func tenantRm(args []string) error {
	pos, purge := takeFlag(args, "--purge", "-purge")
	if len(pos) < 1 {
		return fmt.Errorf("usage: vswarm tenant rm <name> [--purge]")
	}
	name := pos[0]

	c, err := config.Parse(tenantsFile)
	if err != nil {
		return err
	}
	if !c.RemoveTenant(name) {
		return fmt.Errorf("no such tenant %q", name)
	}
	if err := c.Save(); err != nil {
		return err
	}
	_ = dockerx.Compose("rm", "-sf", "vswarm-"+name)
	if err := render.Render(c); err != nil {
		return err
	}
	_, _ = dockerx.Exec(proxyContainer, "angie", "-s", "reload")
	if purge {
		if err := os.RemoveAll(filepath.Join("config", name)); err != nil {
			return err
		}
		fmt.Printf("removed %s and purged its data\n", name)
	} else {
		fmt.Printf("removed %s (data kept in config/%s)\n", name, name)
	}
	return nil
}

func tenantLs() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("%-16s %-30s %-12s\n", "NAME", "EMAIL", "STATUS")
	for _, t := range c.Tenants {
		status := "not-created"
		if out, err := dockerx.Output("docker", "inspect", "-f", "{{.State.Status}}", "vswarm-"+t.Name); err == nil {
			status = strings.TrimSpace(out)
		}
		fmt.Printf("%-16s %-30s %-12s\n", t.Name, t.Email, status)
	}
	return nil
}
