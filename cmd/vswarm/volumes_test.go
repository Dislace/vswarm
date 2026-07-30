package main

import (
	"strings"
	"testing"
)

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

func TestVolumeRunArgsOverridesTheImageEntrypoint(t *testing.T) {
	args := volumeRunArgs("vswarm/workspace:latest", "bash",
		[]string{"-v", "/legacy:/src:ro", "-v", "vswarm-work-x:/dst"},
		[]string{"-c", "echo hi"})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--entrypoint bash") {
		t.Fatalf("missing entrypoint override: %q", joined)
	}

	ep := indexOf(args, "--entrypoint")
	img := indexOf(args, "vswarm/workspace:latest")
	script := indexOf(args, "-c")
	if ep == -1 || img == -1 || script == -1 {
		t.Fatalf("unexpected argv: %q", joined)
	}
	if ep > img {
		t.Errorf("--entrypoint must precede the image or docker ignores it: %q", joined)
	}
	if script < img {
		t.Errorf("command args must follow the image: %q", joined)
	}
}

func indexOf(hay []string, needle string) int {
	for i, s := range hay {
		if s == needle {
			return i
		}
	}
	return -1
}

func TestDeliverScriptResetsVolumeRootAndRequiresBash(t *testing.T) {
	if !strings.Contains(deliverScript, "chown 1000:1000 /dst\n") {
		t.Error("must reset the volume root owner; cp -a inherits the staging dir's")
	}
	if !strings.Contains(deliverScript, "chmod 0755 /dst") {
		t.Error("must reset the volume root mode; cp -a inherits the staging dir's")
	}
	if strings.Contains(deliverScript, "read -r -d ''") && !strings.Contains(deliverScript, "set -euo pipefail") {
		t.Error("script relies on a bashism, so it must be run under bash")
	}
}
