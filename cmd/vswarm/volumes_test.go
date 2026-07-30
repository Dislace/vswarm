package main

import (
	"strings"
	"testing"
)

// These strings are interpolated into an `rm -rf` that runs as root against a
// tenant's work volume. The caller being Ansible is not a reason to trust them.
func TestCheckRelPathRejectsAnythingThatEscapesTheHome(t *testing.T) {
	for _, bad := range []string{
		"",
		"/etc/passwd",
		"..",
		"../../etc",
		".ssh/../../../etc/shadow",
		"a/../..",
	} {
		if err := checkRelPath(bad); err == nil {
			t.Errorf("checkRelPath(%q) = nil, want an error", bad)
		}
	}
	for _, good := range []string{
		".ssh/vswarm-admin",
		".config/dislace/cf-access-ops.env",
		".pg.env",
		"repos/thing",
		"./.ssh/vswarm-admin",
	} {
		if err := checkRelPath(good); err != nil {
			t.Errorf("checkRelPath(%q) = %v, want nil", good, err)
		}
	}
}

// A migration that half-copies and reports success is how the legacy home gets
// deleted with work still in it. The tar pipe must run under bash with pipefail
// so a failing source tar is not masked by the extracting tar's clean exit.
func TestMigrateScriptFailsOnSourceTarError(t *testing.T) {
	script := migrateScript([]string{"--exclude=node_modules"})
	if !strings.Contains(script, "pipefail") {
		t.Errorf("migrate script must set pipefail, got %q", script)
	}
	if !strings.Contains(script, "--exclude=node_modules") {
		t.Errorf("migrate script dropped excludes, got %q", script)
	}
	if !strings.Contains(script, "| tar -C /dst -xf -") {
		t.Errorf("migrate script is no longer a tar pipe, got %q", script)
	}
}
