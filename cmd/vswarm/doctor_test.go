package main

import (
	"errors"
	"strings"
	"testing"
)

// The cache-split check is the only thing that catches a typo'd mount path, so
// it has to fail when it cannot read the device numbers. It previously returned
// "not the same device" on a failed exec, which the caller read as a pass.
func TestInterpretDevices(t *testing.T) {
	for _, tc := range []struct {
		name     string
		out      string
		err      error
		separate bool
	}{
		{name: "distinct devices", out: "45 46\n", separate: true},
		{name: "same device is not a split", out: "45 45\n", separate: false},
		{name: "exec failure is not a split", err: errors.New("no such container"), separate: false},
		{name: "truncated output is not a split", out: "45\n", separate: false},
		{name: "empty output is not a split", out: "", separate: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			separate, detail := interpretDevices(tc.out, tc.err)
			if separate != tc.separate {
				t.Fatalf("separate = %v, want %v (detail %q)", separate, tc.separate, detail)
			}
			if !separate && strings.TrimSpace(detail) == "" && tc.out != "" {
				t.Errorf("a failing check should explain itself, got empty detail")
			}
		})
	}
}
