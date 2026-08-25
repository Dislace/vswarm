package main

import (
	"errors"
	"testing"
)

func TestInterpretMounts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     string
		err     error
		mounted bool
	}{
		{
			name: "cache mounted",
			out: "/dev/sdb /home/ai-agent ext4 rw 0 0\n" +
				"/dev/sdb /home/ai-agent/.cache ext4 rw 0 0\n",
			mounted: true,
		},
		{
			name:    "cache not mounted",
			out:     "/dev/sdb /home/ai-agent ext4 rw 0 0\n",
			mounted: false,
		},
		{
			name:    "typo'd mount path",
			out:     "/dev/sdb /home/ai-agent ext4 rw 0 0\n/dev/sdb /home/ai-agent/.cach ext4 rw 0 0\n",
			mounted: false,
		},
		{name: "exec failure", err: errors.New("no such container"), mounted: false},
		{name: "empty output", out: "", mounted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mounted, detail := interpretMounts(tc.out, tc.err)
			if mounted != tc.mounted {
				t.Fatalf("mounted = %v, want %v (detail %q)", mounted, tc.mounted, detail)
			}
			if !mounted && detail == "" {
				t.Error("a failing check should explain itself")
			}
		})
	}
}

// A negative assertion -- "A cannot reach B's db", "this home has no admin key"
// -- is only proven by a probe that actually ran. Before this, both were read
// off an exit code, so a missing python3, a missing stat or a stopped container
// produced [PASS] on an isolation claim nobody had checked.
//
// Dislace/core gates the fleet converge on doctor's exit code
// (roles/vswarm/tasks/main.yml, `until: vswarm_doctor.rc == 0`), so the false
// PASS was load-bearing beyond the console.
func TestInterpretProbe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		out   string
		err   error
		want  probe
		prove bool
	}{
		{name: "reached", out: "REACHED\n", want: probeYes, prove: false},
		{name: "blocked", out: "BLOCKED [Errno 111] Connection refused\n", want: probeNo, prove: true},
		{
			name: "interpreter missing",
			err:  errors.New(`exec: "python3": executable file not found in $PATH`),
			want: probeUnknown, prove: false,
		},
		{
			name: "container not running",
			err:  errors.New("Error: No such container: vswarm-demo"),
			want: probeUnknown, prove: false,
		},
		{name: "no verdict", out: "", want: probeUnknown, prove: false},
		{name: "unexpected chatter", out: "Traceback (most recent call last):\n", want: probeUnknown, prove: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := interpretProbe(tc.out, tc.err, "REACHED", "BLOCKED")
			if got != tc.want {
				t.Fatalf("probe = %v, want %v (detail %q)", got, tc.want, detail)
			}
			if got.provesAbsence() != tc.prove {
				t.Fatalf("provesAbsence = %v, want %v", got.provesAbsence(), tc.prove)
			}
			if got == probeUnknown && detail == "" {
				t.Error("an inconclusive probe must explain why it could not answer")
			}
		})
	}
}

// The regression in one line: a failing probe must never satisfy a check that
// asserts something is absent.
func TestAnInconclusiveProbeNeverProvesAbsence(t *testing.T) {
	for _, err := range []error{
		errors.New(`exec: "python3": executable file not found in $PATH`),
		errors.New("Error response from daemon: container is not running"),
		errors.New("context deadline exceeded"),
	} {
		got, _ := interpretProbe("", err, "REACHED", "BLOCKED")
		if got.provesAbsence() {
			t.Fatalf("%v was read as proof of isolation", err)
		}
	}
}

// interpretProbe is shared, so the sentinels are per-call rather than global.
func TestInterpretProbeUsesTheCallersSentinels(t *testing.T) {
	got, _ := interpretProbe("ABSENT\n", nil, "PRESENT", "ABSENT")
	if got != probeNo || !got.provesAbsence() {
		t.Fatalf("ABSENT should prove absence, got %v", got)
	}
	got, _ = interpretProbe("PRESENT\n", nil, "PRESENT", "ABSENT")
	if got != probeYes || got.provesAbsence() {
		t.Fatalf("PRESENT must not prove absence, got %v", got)
	}
}
