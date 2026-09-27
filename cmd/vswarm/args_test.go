package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// runIn runs the CLI in an empty directory and reports what it left behind.
// With no tenants.yaml there, a subcommand that got past the guard fails
// instead of reaching docker.
func runIn(t *testing.T, args ...string) (code int, left []os.DirEntry) {
	t.Helper()
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	code = run(args)
	left, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return code, left
}

func TestHelpPrintsTheSubcommandUsageAndDoesNothingElse(t *testing.T) {
	for _, argv := range [][]string{
		{"up", "--help"},
		{"up", "--json", "-h"},
		{"down", "--help"},
		{"logs", "--help"},
		{"pair", "--help"},
		{"provision", "--help"},
		{"provision", "alex", "--from", "stage", "--help"},
		{"init", "--help"},
		{"tenant", "--help"},
		{"tenant", "add", "-h"},
		{"doctor", "--help"},
	} {
		code, left := runIn(t, argv...)
		if code != 0 {
			t.Errorf("vswarm %s exited %d, want 0", strings.Join(argv, " "), code)
		}
		if len(left) != 0 {
			t.Errorf("vswarm %s left %d entries behind; help must have no side effects", strings.Join(argv, " "), len(left))
		}
	}

	help, err := checkArgs("up", []string{"--help"})
	if err != nil || !strings.Contains(help, "usage: vswarm up [--json]") {
		t.Errorf("up --help = %q, %v; want the up synopsis", help, err)
	}
	help, _ = checkArgs("tenant", []string{"rm", "--help"})
	if !strings.Contains(help, "usage: vswarm tenant rm <name> [--purge]") {
		t.Errorf("tenant rm --help = %q, want the tenant rm synopsis", help)
	}
}

func TestAnArgumentTheSubcommandDoesNotTakeIsAUsageError(t *testing.T) {
	for _, argv := range [][]string{
		{"up", "--bogus"},
		{"down", "--force"},
		{"down", "extra"},
		{"logs", "alex", "bob"},
		{"pair"},
		{"provision", "alex", "--form", "stage"},
		{"provision", "alex", "--from"},
		{"doctor", "--wait", "30s"},
		{"tenant"},
		{"tenant", "ls", "--purge"},
	} {
		code, left := runIn(t, argv...)
		if code != 2 {
			t.Errorf("vswarm %s exited %d, want 2", strings.Join(argv, " "), code)
		}
		if len(left) != 0 {
			t.Errorf("vswarm %s left %d entries behind; a rejected command line must not run", strings.Join(argv, " "), len(left))
		}
		name, rest := argv[0], argv[1:]
		var ue *usageError
		if _, err := checkArgs(name, rest); !errors.As(err, &ue) {
			t.Errorf("checkArgs(%v) = %v, want a usage error", argv, err)
		}
	}
}

func TestTheCommandLinesDeploymentsRunPassTheGuard(t *testing.T) {
	for _, argv := range [][]string{
		{"up", "--json"},
		{"provision", "alex", "--from", "/var/lib/vswarm/stage/alex"},
		{"provision", "alex", "--remove", ".old", "--remove", ".older"},
		{"doctor", "--wait=60s"},
		{"status", "--json"},
		{"logs", "alex"},
		{"tenant", "add", "a@example.com", "alex", "--no-up"},
		{"tenant", "rm", "alex", "--purge"},
		{"tenant", "remove", "alex"},
		{"tenant", "ls"},
		{"migrate", "alex", "--keep-derived"},
	} {
		if help, err := checkArgs(argv[0], argv[1:]); help != "" || err != nil {
			t.Errorf("checkArgs(%v) = %q, %v; want it let through", argv, help, err)
		}
	}
}
