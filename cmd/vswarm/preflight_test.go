package main

import (
	"net/http"
	"strings"
	"testing"
)

// The proxy answers a preflight 204, so a status from the public name that is not
// 204 came from whatever sits in front of it — and that is the one failure an
// operator cannot see from the host, so the detail has to name the setting.
func TestClassifyEdgePreflight(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		pass   bool
	}{
		{"proxy answered it", http.StatusNoContent, true},
		{"access rejected it", http.StatusForbidden, false},
		{"access redirected it to a login", http.StatusFound, false},
		{"nothing is listening", http.StatusBadGateway, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyEdgePreflight("code.example.com", tc.status)
			if got.pass != tc.pass {
				t.Fatalf("pass = %v, want %v", got.pass, tc.pass)
			}
			if got.name != edgePreflightCheck {
				t.Errorf("name = %q, want %q", got.name, edgePreflightCheck)
			}
			if tc.pass {
				return
			}
			if !strings.Contains(got.detail, "options_preflight_bypass") {
				t.Errorf("detail does not name the setting to change: %q", got.detail)
			}
			if !strings.Contains(got.detail, "code.example.com") {
				t.Errorf("detail does not name the host it asked: %q", got.detail)
			}
		})
	}
}
