package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/render"
)

// t3's service launcher starts the server with no arguments of its own, so the
// image's environment is the whole launch configuration. Getting one of these
// wrong does not fail the build -- it produces a workspace the proxy cannot
// reach, and the port is the one constant the committed image shares with the
// rendered compose file.
func TestCommittedImageServesWhereTheProxyRoutes(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", imageContext, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"T3CODE_MODE=web",
		"T3CODE_HOST=0.0.0.0",
		"T3CODE_PORT=" + render.T3Port,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("Dockerfile does not set %s", want)
		}
	}
}

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
