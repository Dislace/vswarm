package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dislace/vswarm/internal/config"
)

// init must not name an image for the operator: a floating tag is whatever
// the registry says today, and render would accept it.
func TestInitScaffoldIsRefusedUntilAnImageIsNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), tenantsFile)
	if err := os.WriteFile(path, []byte(defaultTenants), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(path)
	if err != nil {
		t.Fatalf("the scaffold must parse: %v", err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Fatalf("Validate() = %v, want the scaffold refused with the missing-image message", err)
	}
}

// fakeDocker puts a docker on PATH that records each command line and exits
// with status, and runs the test from an empty directory holding roster.
func fakeDocker(t *testing.T, status int, roster string) (calls func() []string) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >>%s\nexit %d\n", log, status)
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.WriteFile(tenantsFile, []byte(roster), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		raw, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

func TestATenantThatStillAsksForThePlaywrightSidecarIsRefused(t *testing.T) {
	const head = "domain: code.example.com\nimage: vswarm/workspace:test\n"
	for name, roster := range map[string]string{
		"service": head + "tenants:\n  - email: a@example.com\n    name: a\n    services: [postgres, playwright]\n",
		"with the image": head + "playwright_image: zenika/alpine-chrome:124\ntenants:\n" +
			"  - email: a@example.com\n    name: a\n    services: [playwright]\n",
	} {
		t.Run(name, func(t *testing.T) {
			calls := fakeDocker(t, 1, roster)
			for cmd, run := range map[string]func() error{
				"render": cmdRender,
				"up":     func() error { return cmdUp(nil) },
			} {
				err := run()
				if err == nil || !strings.Contains(err.Error(), "Playwright sidecar was removed") ||
					!strings.Contains(err.Error(), config.BrowsersDir) {
					t.Errorf("%s: error = %v; want a refusal naming the removal and the baked browser", cmd, err)
				}
			}
			if _, err := os.Stat(filepath.Join("generated", "docker-compose.yml")); err == nil {
				t.Error("a compose file was rendered from a roster that was refused")
			}
			if got := calls(); len(got) != 1 || got[0] != "" {
				t.Errorf("docker was called: %q", got)
			}
		})
	}
}

// The sidecar a roster used to declare is not in the compose file any more,
// and only --remove-orphans makes compose stop and remove it.
func TestUpRemovesContainersTheComposeFileNoLongerDeclares(t *testing.T) {
	calls := fakeDocker(t, 0, "domain: code.example.com\nimage: vswarm/workspace:test\ntenants:\n")
	if err := cmdUp(nil); err != nil {
		t.Fatalf("cmdUp() error = %v", err)
	}
	want := "compose --project-directory . -f generated/docker-compose.yml up -d --remove-orphans"
	for _, c := range calls() {
		if c == want {
			return
		}
	}
	t.Fatalf("docker calls = %q, want %q", calls(), want)
}

// captureStderr returns what run wrote to stderr.
func captureStderr(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()
	run()
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

// Older `tenant add/rm` saved playwright_image into every roster, so the key
// alone says nothing about whether the sidecar was used.
const staleImageRoster = "domain: code.example.com\nimage: vswarm/workspace:test\n" +
	"playwright_image: zenika/alpine-chrome:124\ntenants:\n  - email: a@example.com\n    name: a\n"

func TestAStrayPlaywrightImageIsIgnoredWithAWarning(t *testing.T) {
	fakeDocker(t, 1, staleImageRoster)
	var err error
	stderr := captureStderr(t, func() { err = cmdRender() })
	if err != nil {
		t.Fatalf("render error = %v; a roster that only carries playwright_image must still render", err)
	}
	for _, want := range []string{"warning: " + tenantsFile + ":3: playwright_image:",
		"Playwright sidecar was removed", config.BrowsersDir, "ignored; delete the line"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	if _, err := os.Stat(filepath.Join("generated", "docker-compose.yml")); err != nil {
		t.Errorf("no compose file rendered: %v", err)
	}
}

func TestTenantAddStopsSavingPlaywrightImage(t *testing.T) {
	fakeDocker(t, 1, staleImageRoster)
	captureStderr(t, func() {
		if err := tenantAdd([]string{"b@example.com", "b", "--no-up"}); err != nil {
			t.Fatalf("tenant add error = %v", err)
		}
	})
	saved, err := os.ReadFile(tenantsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "playwright_image") {
		t.Errorf("tenant add saved playwright_image back:\n%s", saved)
	}
	if stderr := captureStderr(t, func() { _, _ = parseConfig() }); stderr != "" {
		t.Errorf("the saved roster still warns: %q", stderr)
	}
}
