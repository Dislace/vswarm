package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/dislace/vswarm/internal/config"
	"github.com/dislace/vswarm/internal/dockerx"
)

// humanOut is where progress prose goes. `--json` points it at stderr so the
// only thing on stdout is the document the caller is parsing.
var humanOut io.Writer = os.Stdout

// stackContainers is every container the rendered compose file declares, in a
// stable order. It comes from the roster rather than from docker so a
// container that failed to come up is reported as absent instead of omitted.
func stackContainers(c *config.Config) []string {
	names := []string{proxyContainer}
	if c.ManageTunnel {
		names = append(names, "vswarm-tunnel")
	}
	for _, t := range c.Tenants {
		names = append(names, "vswarm-"+t.Name)
		if t.HasService("postgres") {
			names = append(names, "vswarm-db-"+t.Name)
		}
		if t.HasService("playwright") {
			names = append(names, "vswarm-playwright-"+t.Name)
		}
	}
	return names
}

// containerIDs maps container name to id for everything on the host. It is one
// listing rather than an inspect per name because a name that does not exist
// yet is the ordinary case before `up`, and inspect spends an error saying so.
func containerIDs() map[string]string {
	out, err := dockerx.Output("docker", "ps", "-a", "--no-trunc", "--format", "{{.Names}} {{.ID}}")
	if err != nil {
		return nil
	}
	ids := map[string]string{}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if name, id, ok := strings.Cut(strings.TrimSpace(ln), " "); ok {
			ids[name] = id
		}
	}
	return ids
}

// upChange is what `up` did to one container. The consuming deployment layer
// used to diff image and container ids around `up` itself, purely because
// compose reports this in prose.
type upChange struct {
	Container string `json:"container"`
	Action    string `json:"action"`
	ID        string `json:"id,omitempty"`
}

// upReport is Changed plus the evidence for it. Changed covers anything that
// is not "unchanged", absent included: a declared container that `up` did not
// leave running is not a converged stack.
type upReport struct {
	Changed    bool       `json:"changed"`
	Containers []upChange `json:"containers"`
}

func classifyUp(names []string, before, after map[string]string) upReport {
	r := upReport{Containers: make([]upChange, 0, len(names))}
	for _, name := range names {
		id, present := after[name]
		ch := upChange{Container: name, ID: id}
		switch {
		case !present:
			ch.Action = "absent"
		case before[name] == "":
			ch.Action = "created"
		case before[name] != id:
			ch.Action = "recreated"
		default:
			ch.Action = "unchanged"
		}
		if ch.Action != "unchanged" {
			r.Changed = true
		}
		r.Containers = append(r.Containers, ch)
	}
	return r
}

type statusEntry struct {
	Container string `json:"container"`
	ID        string `json:"id,omitempty"`
	State     string `json:"state"`
	Health    string `json:"health,omitempty"`
}

func stackStatus(c *config.Config) []statusEntry {
	names := stackContainers(c)
	out := make([]statusEntry, len(names))
	_ = runParallel(len(names), func(i int) error {
		out[i] = inspectStatus(names[i])
		return nil
	})
	return out
}

func inspectStatus(name string) statusEntry {
	raw, err := dockerx.Output("docker", "inspect", "-f",
		"{{.Id}} {{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", name)
	if err != nil {
		return statusEntry{Container: name, State: "absent"}
	}
	f := strings.Fields(raw)
	e := statusEntry{Container: name, State: "unknown"}
	if len(f) > 0 {
		e.ID = f[0]
	}
	if len(f) > 1 {
		e.State = f[1]
	}
	if len(f) > 2 {
		e.Health = f[2]
	}
	return e
}

// takeJSON pulls --json out of an argument list and, when present, moves
// progress prose to stderr before anything has written to stdout.
func takeJSON(args []string) ([]string, bool) {
	rest, found := takeFlag(args, "--json")
	if found {
		humanOut = os.Stderr
	}
	return rest, found
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
