package dockerx

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const composeFile = "generated/docker-compose.yml"

func Compose(args ...string) error { return ComposeTo(os.Stdout, args...) }

// ComposeTo runs compose with its stdout somewhere the caller chooses, so a
// command reporting JSON on stdout can keep compose's chatter off it.
func ComposeTo(stdout io.Writer, args ...string) error {
	full := append([]string{"compose", "--project-directory", ".", "-f", composeFile}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func Exec(container string, args ...string) (string, error) {
	full := append([]string{"exec", container}, args...)
	return Output("docker", full...)
}

func Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Output(name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %v: %v: %s", name, args, err, errb.String())
	}
	return out.String(), nil
}
