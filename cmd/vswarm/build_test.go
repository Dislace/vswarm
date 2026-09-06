package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dislace/vswarm/internal/config"
)

// The build context is committed rather than rendered, so nothing regenerates
// it from the constants the compose file uses. These two have to be checked
// against each other directly: a cache directory the image never creates is
// one the tenant's cache volume mounts over as root.
func TestCommittedImageAgreesWithTheCacheConstant(t *testing.T) {
	for _, f := range []string{"Dockerfile", "entrypoint.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", imageContext, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, sub := range []string{"npm", "bun", "go/mod", "go/build", "pip"} {
			if !strings.Contains(string(body), config.CacheDir+"/"+sub) {
				t.Errorf("%s does not create %s/%s", f, config.CacheDir, sub)
			}
		}
	}
}
