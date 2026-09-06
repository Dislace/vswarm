package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/dislace/vswarm/internal/config"
)

func TestClassifyUpSeparatesRecreatedFromLeftAlone(t *testing.T) {
	names := []string{"vswarm-proxy", "vswarm-alice", "vswarm-bob", "vswarm-db-bob"}
	before := map[string]string{
		"vswarm-proxy": "p1",
		"vswarm-alice": "a1",
		"vswarm-bob":   "b1",
	}
	after := map[string]string{
		"vswarm-proxy": "p1",
		"vswarm-alice": "a2",
		"vswarm-bob":   "b1",
		"vswarm-carol": "c1",
	}

	got := classifyUp(names, before, after)
	want := []upChange{
		{Container: "vswarm-proxy", Action: "unchanged", ID: "p1"},
		{Container: "vswarm-alice", Action: "recreated", ID: "a2"},
		{Container: "vswarm-bob", Action: "unchanged", ID: "b1"},
		{Container: "vswarm-db-bob", Action: "absent"},
	}
	if !reflect.DeepEqual(got.Containers, want) {
		t.Fatalf("classifyUp() = %#v, want %#v", got.Containers, want)
	}
	if !got.Changed {
		t.Error("a recreated container means the stack changed")
	}
	// A container nobody declared is not this stack's business, even when it
	// is sitting on the same host.
	for _, ch := range got.Containers {
		if ch.Container == "vswarm-carol" {
			t.Error("classifyUp reported a container the roster does not declare")
		}
	}
}

func TestClassifyUpReportsNoChangeWhenNothingMoved(t *testing.T) {
	ids := map[string]string{"vswarm-proxy": "p1", "vswarm-alice": "a1"}
	got := classifyUp([]string{"vswarm-proxy", "vswarm-alice"}, ids, ids)
	if got.Changed {
		t.Fatalf("a re-run that recreated nothing must report changed=false: %#v", got)
	}
}

func TestClassifyUpCallsAFirstAppearanceCreated(t *testing.T) {
	got := classifyUp([]string{"vswarm-alice"}, nil, map[string]string{"vswarm-alice": "a1"})
	if got.Containers[0].Action != "created" || !got.Changed {
		t.Fatalf("first appearance = %#v, want created and changed", got)
	}
}

func TestStackContainersFollowsTheRosterNotTheHost(t *testing.T) {
	c := &config.Config{
		ManageTunnel: true,
		Tenants: []config.Tenant{
			{Name: "alice", Email: "alice@example.com", Services: []string{"postgres"}},
			{Name: "bob", Email: "bob@example.com", Services: []string{"playwright"}},
		},
	}
	want := []string{
		"vswarm-proxy",
		"vswarm-tunnel",
		"vswarm-alice",
		"vswarm-db-alice",
		"vswarm-bob",
		"vswarm-playwright-bob",
	}
	if got := stackContainers(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("stackContainers() = %v, want %v", got, want)
	}

	c.ManageTunnel = false
	for _, n := range stackContainers(c) {
		if n == "vswarm-tunnel" {
			t.Error("the tunnel is not declared when manage_tunnel is false")
		}
	}
}

func TestTakeDurationParsesTheWaitAndLeavesTheRest(t *testing.T) {
	rest, d, err := takeDuration([]string{"--wait=30s"}, "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if d != 30*time.Second || len(rest) != 0 {
		t.Fatalf("takeDuration() = %v, %v, want 30s and nothing left", rest, d)
	}

	rest, d, err = takeDuration([]string{"extra"}, "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if d != 0 {
		t.Errorf("absent --wait = %v, want 0 so doctor runs one pass as before", d)
	}
	if !reflect.DeepEqual(rest, []string{"extra"}) {
		t.Errorf("rest = %v, want the unrecognised argument kept so it can be refused", rest)
	}

	if _, _, err := takeDuration([]string{"--wait=soon"}, "--wait"); err == nil {
		t.Error("an unparseable duration must not silently become zero")
	}
}

func TestDoctorPassedIgnoresNotApplicableChecks(t *testing.T) {
	if !doctorPassed([]checkResult{{name: "a", pass: true}, {}}) {
		t.Error("a zero checkResult means not applicable and must not fail the run")
	}
	if doctorPassed([]checkResult{{name: "a", pass: true}, {name: "b"}}) {
		t.Error("a named failing check must fail the run")
	}
}
