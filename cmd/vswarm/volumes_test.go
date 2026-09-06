package main

import (
	"os"
	"path/filepath"
	"reflect"
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
	if !strings.Contains(deliverScript, "cp -a --no-preserve=ownership /src/. /dst/") {
		t.Error("cp must not stamp the staging dir's ownership onto directories the tenant already owns; a live workspace loses access to everything beneath them")
	}
	if strings.Contains(deliverScript, "read -r -d ''") && !strings.Contains(deliverScript, "set -euo pipefail") {
		t.Error("script relies on a bashism, so it must be run under bash")
	}
}

func TestStagedPathsListsFilesRelativeToTheStageRoot(t *testing.T) {
	stage := t.TempDir()
	for _, p := range []string{".pg.env", ".config/vswarm/repos", ".ssh/vswarm-admin"} {
		full := filepath.Join(stage, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(stage, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := stagedPaths(stage)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".config/vswarm/repos", ".pg.env", ".ssh/vswarm-admin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stagedPaths() = %v, want %v (directories must not be listed; removing one takes the tenant's files with it)", got, want)
	}
}

func TestStaleProvisionedNeverNamesAPathVswarmDidNotDeliver(t *testing.T) {
	previous := []string{".pg.env", ".config/dislace/retired.env"}
	staged := []string{".pg.env", ".config/vswarm/repos"}

	got := staleProvisioned(previous, staged)
	if !reflect.DeepEqual(got, []string{".config/dislace/retired.env"}) {
		t.Fatalf("staleProvisioned() = %v, want only the path that left the staging tree", got)
	}

	// The safety property: everything eligible for removal came from the
	// previous list. A file the tenant made is not in it and cannot be named.
	was := map[string]bool{}
	for _, p := range previous {
		was[p] = true
	}
	for _, p := range got {
		if !was[p] {
			t.Fatalf("staleProvisioned() named %q, which vswarm never delivered", p)
		}
	}
}

func TestStaleProvisionedDropsEntriesThatEscapeTheTenantHome(t *testing.T) {
	// The list lives in a volume the tenant can write, so a tampered entry
	// must not turn provisioning into an arbitrary delete.
	previous := []string{
		"/etc/passwd",
		"../../etc/shadow",
		"..",
		".ssh/../../../root/.ssh/authorized_keys",
		".pg.env",
	}
	got := staleProvisioned(previous, nil)
	if !reflect.DeepEqual(got, []string{".pg.env"}) {
		t.Fatalf("staleProvisioned() = %v, want only the entry inside the tenant home", got)
	}
}

func TestProvisionedListRoundTripsAndSkipsItsOwnHeader(t *testing.T) {
	paths := []string{".config/vswarm/repos", ".pg.env"}
	got := parseProvisioned(formatProvisioned(paths))
	if !reflect.DeepEqual(got, paths) {
		t.Fatalf("round trip = %v, want %v", got, paths)
	}
	if parseProvisioned(formatProvisioned(nil)) != nil {
		t.Error("an empty list must parse back to nothing, not to a header line")
	}
	if got := parseProvisioned("  .pg.env  \n\n# a comment\n"); !reflect.DeepEqual(got, []string{".pg.env"}) {
		t.Errorf("parseProvisioned() = %v, want the blank line and comment dropped", got)
	}
}

func TestRevokeFilesNeverRecurses(t *testing.T) {
	// A provisioned path that is a directory today is one the tenant made.
	args := volumeRunArgs("vswarm/workspace:latest", "rm",
		[]string{"-v", "vswarm-work-x:/dst"}, []string{"-f", "/dst/.pg.env"})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-rf") || strings.Contains(joined, "-r ") {
		t.Fatalf("declarative removal must not recurse: %q", joined)
	}
	if !strings.Contains(joined, "-f /dst/.pg.env") {
		t.Fatalf("unexpected argv: %q", joined)
	}
}

func TestWriteProvisionedKeepsTheListOutOfItsOwnContents(t *testing.T) {
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, ".pg.env"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged, err := stagedPaths(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeProvisioned(stage, staged); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(provisionedList)))
	if err != nil {
		t.Fatal(err)
	}
	// Listing itself would make the list stale the moment it moved, and the
	// next provision would delete the record of what it had delivered.
	for _, p := range parseProvisioned(string(body)) {
		if p == provisionedList {
			t.Fatal("the list must not name itself")
		}
	}
}
