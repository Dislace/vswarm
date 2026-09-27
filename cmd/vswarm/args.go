package main

import (
	"fmt"
	"strings"
)

// command is what a subcommand accepts on its command line. It is checked
// before the subcommand runs, so `up --help` prints help instead of deploying
// and a mistyped flag stops the command instead of being dropped.
type command struct {
	name     string
	aliases  []string
	synopsis string
	// summary is this command's entry under COMMANDS in the top-level usage,
	// verbatim, so the two texts cannot drift.
	summary string

	flags    []string // stand alone
	valued   []string // take the next argument as their value
	prefixed []string // carry their value after "=", like --wait=30s
	min, max int      // positional arguments
}

var commands = []command{
	{name: "init", synopsis: "vswarm init",
		summary: "  init                     scaffold tenants.yaml, .env, config/ (idempotent)\n"},
	{name: "render", synopsis: "vswarm render",
		summary: "  render                   tenants.yaml -> generated/ (compose + angie)\n"},
	{name: "build", synopsis: "vswarm build",
		summary: "  build                    build ./image and tag it with the image: from\n" +
			"                           tenants.yaml (vswarm checkout only; hosts pull)\n"},
	{name: "up", synopsis: "vswarm up [--json]", flags: []string{"--json"},
		summary: "  up                       render, start the stack, provision + pair every tenant\n" +
			"                           (--json reports what each container did: created,\n" +
			"                            recreated, unchanged or absent)\n"},
	{name: "down", synopsis: "vswarm down",
		summary: "  down                     stop the stack\n"},
	{name: "tenant", synopsis: "vswarm tenant <add|rm|ls> ...", min: 1, max: 1},
	{name: "tenant add", synopsis: "vswarm tenant add <email> <name> [--no-up]",
		flags: []string{"--no-up", "-no-up"}, min: 2, max: 2,
		summary: "  tenant add <email> <name>   add a tenant; start + pair it   (--no-up to skip)\n"},
	{name: "tenant rm", aliases: []string{"tenant remove"}, synopsis: "vswarm tenant rm <name> [--purge]",
		flags: []string{"--purge", "-purge"}, min: 1, max: 1,
		summary: "  tenant rm <name>            remove a tenant                  (--purge to wipe data)\n"},
	{name: "tenant ls", aliases: []string{"tenant list"}, synopsis: "vswarm tenant ls",
		summary: "  tenant ls                   list tenants + container status\n"},
	{name: "pair", synopsis: "vswarm pair <name>", min: 1, max: 1,
		summary: "  pair <name>              reconcile a tenant to one T3 session: reuse it while it\n" +
			"                           has life left, mint when it does not, revoke the rest,\n" +
			"                           inject it into angie and deliver it to the workspace\n"},
	{name: "provision", synopsis: "vswarm provision <name> [--from <dir>] [--remove <rel-path>]...",
		valued: []string{"--from", "--remove"}, min: 1, max: 1,
		summary: "  provision <name>         make a tenant's work volume match a staging tree\n" +
			"                           (--from <dir> is the desired state: what it holds is\n" +
			"                            delivered, what vswarm delivered before and it no\n" +
			"                            longer holds is taken back, and nothing the tenant\n" +
			"                            made is touched. Without --from only the files\n" +
			"                            tenants.yaml produces are delivered or taken back.\n" +
			"                            --remove <rel-path> is an escape hatch for paths\n" +
			"                            vswarm never delivered, repeatable)\n"},
	{name: "migrate", synopsis: "vswarm migrate <name> [--keep-derived]",
		flags: []string{"--keep-derived"}, min: 1, max: 1,
		summary: "  migrate <name>           copy a legacy config/<name>/home bind mount into the\n" +
			"                           work volume, dropping rebuildable caches\n" +
			"                           (--keep-derived copies them too)\n"},
	{name: "status", synopsis: "vswarm status [--json]", flags: []string{"--json"},
		summary: "  status                   docker compose ps                       (--json)\n"},
	{name: "logs", synopsis: "vswarm logs [tenant]", max: 1,
		summary: "  logs [tenant]            follow logs (proxy by default)\n"},
	{name: "doctor", synopsis: "vswarm doctor [--wait=<duration>]", prefixed: []string{"--wait="},
		summary: "  doctor                   verify isolation + config invariants\n" +
			"                           (--wait=30s retries until they pass or time out)\n"},
	{name: "version", synopsis: "vswarm version",
		summary: "  version                  print the release this binary was built from\n"},
}

// usageError is a command line the subcommand cannot act on. It exits 2, as
// an unknown command does, so a caller can tell a typo from a failed run.
type usageError struct{ msg, synopsis string }

func (e *usageError) Error() string {
	return fmt.Sprintf("%s\nusage: %s", e.msg, e.synopsis)
}

// checkArgs vets a subcommand's arguments before it runs. help is the text to
// print when -h/--help was asked for; err is a *usageError when the arguments
// cannot mean anything to the subcommand. A name it does not know is left to
// the dispatcher, which reports it.
func checkArgs(name string, args []string) (help string, err error) {
	if name == "tenant" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if _, ok := lookup("tenant " + args[0]); !ok {
			return "", nil
		}
		name, args = "tenant "+args[0], args[1:]
	}
	c, ok := lookup(name)
	if !ok {
		return "", nil
	}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return c.help(), nil
		case contains(c.flags, a):
		case contains(c.valued, a):
			if i+1 >= len(args) {
				return "", &usageError{a + " needs a value", c.synopsis}
			}
			i++
		case hasPrefixIn(a, c.prefixed):
		case strings.HasPrefix(a, "-"):
			return "", &usageError{fmt.Sprintf("unknown flag %q for %s", a, c.name), c.synopsis}
		default:
			pos = append(pos, a)
		}
	}
	switch {
	case len(pos) < c.min:
		return "", &usageError{"missing arguments", c.synopsis}
	case len(pos) > c.max:
		return "", &usageError{fmt.Sprintf("unexpected argument %q", pos[c.max]), c.synopsis}
	}
	return "", nil
}

func (c command) help() string {
	if c.name == "tenant" {
		var b strings.Builder
		b.WriteString("usage: " + c.synopsis + "\n\n")
		for _, sub := range commands {
			if strings.HasPrefix(sub.name, "tenant ") {
				b.WriteString(sub.summary)
			}
		}
		return b.String()
	}
	return "usage: " + c.synopsis + "\n\n" + c.summary
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name || contains(c.aliases, name) {
			return c, true
		}
	}
	return command{}, false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasPrefixIn(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
