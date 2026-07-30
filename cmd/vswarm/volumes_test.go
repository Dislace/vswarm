package main

import "testing"

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
