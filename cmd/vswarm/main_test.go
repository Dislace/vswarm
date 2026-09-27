package main

import (
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
