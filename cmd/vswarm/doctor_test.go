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
