package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const composeFile = "generated/docker-compose.yml"

// A docker CLI call that never returns stops `up` with it, and a deployment
// run then hangs until someone notices. Every call gets a deadline sized to
// what it does; NoDeadline is for commands that are meant to run until the
// operator stops them, or whose length is the size of the data they move.
const (
	// QueryTimeout bounds a question the daemon answers from its own state:
	// inspect, ps, a short exec.
	QueryTimeout = 60 * time.Second
	// StackTimeout bounds work that can pull images or start containers:
	// compose up/down/rm, and throwaway containers against a volume.
	StackTimeout = 10 * time.Minute
	NoDeadline   = time.Duration(0)
)

// ErrTimeout marks a call abandoned at its deadline.
var ErrTimeout = errors.New("timed out")

// waitDelay bounds how long a killed command's orphaned children may hold its
// output pipes open before Wait gives up on them.
const waitDelay = 5 * time.Second

func Compose(timeout time.Duration, args ...string) error {
	return ComposeTo(timeout, os.Stdout, args...)
}

// ComposeTo runs compose with its stdout somewhere the caller chooses, so a
// command reporting JSON on stdout can keep compose's chatter off it.
func ComposeTo(timeout time.Duration, stdout io.Writer, args ...string) error {
	full := append([]string{"compose", "--project-directory", ".", "-f", composeFile}, args...)
	cmd, done := command(timeout, "docker compose "+firstArg(args), "docker", full)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return done(cmd.Run())
}

func Exec(container string, args ...string) (string, error) {
	full := append([]string{"exec", container}, args...)
	return Output("docker", full...)
}

func Run(timeout time.Duration, name string, args ...string) error {
	cmd, done := command(timeout, name+" "+firstArg(args), name, args)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return done(cmd.Run())
}

func Output(name string, args ...string) (string, error) {
	return OutputWithin(QueryTimeout, name, args...)
}

func OutputWithin(timeout time.Duration, name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd, done := command(timeout, name+" "+firstArg(args), name, args)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := done(cmd.Run()); err != nil {
		return out.String(), fmt.Errorf("%s %v: %w: %s", name, args, err, errb.String())
	}
	return out.String(), nil
}

// ExecStdin feeds a command's stdin instead of passing the value in argv,
// which every process in the container can read. Callers deliver secrets
// this way.
func ExecStdin(container, stdin string, args ...string) (string, error) {
	full := append([]string{"exec", "-i", container}, args...)
	var out, errb bytes.Buffer
	cmd, done := command(QueryTimeout, "docker exec "+container, "docker", full)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := done(cmd.Run()); err != nil {
		return out.String(), fmt.Errorf("docker exec %s: %w: %s", container, err, errb.String())
	}
	return out.String(), nil
}

// command builds a command that is killed at its deadline. done releases the
// deadline and, when it was the deadline that ended the command, says so:
// "signal: killed" alone reads like the daemon's doing, not ours.
func command(timeout time.Duration, label, name string, args []string) (*exec.Cmd, func(error) error) {
	ctx, cancel := context.Background(), context.CancelFunc(func() {})
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = waitDelay
	return cmd, func(err error) error {
		defer cancel()
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s: no result after %s: %w", label, timeout, ErrTimeout)
		}
		return err
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
