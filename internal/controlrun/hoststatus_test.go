package controlrun

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

var boardAt = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func operatorRead(t *testing.T, s control.Service) control.HostStatus {
	t.Helper()
	out, err := s.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "host-status"})
	if err != nil || out.Fleet == nil {
		t.Fatalf("host-status: %+v, %v", out, err)
	}
	return *out.Fleet
}

func TestAPassStoresTheFleetAndTheOpReturnsIt(t *testing.T) {
	c, rh, _, task, _ := readySetup(t)
	h := rh.host
	board := &HostBoard{Now: func() time.Time { return boardAt }}
	c.Board = board
	c.Service.Hosts = board.Status
	h.status.Slots, h.status.HeavySlots = 2, 1
	h.status.Quota = map[string]session.Quota{"claude": {Harness: "claude", Windows: map[string]session.QuotaWindow{"5h": {Used: 40, ResetsAt: 1700000000}}}}
	h.status.SignedOut = []string{"codex"}

	if got := operatorRead(t, c.Service); !got.At.IsZero() || got.Hosts == nil || len(got.Hosts) != 0 {
		t.Fatalf("before any pass: %+v", got)
	}
	// The first pass starts the probe, the second launches the task and the
	// third reads its window.
	for range 3 {
		if err := c.Pass(context.Background()); err != nil {
			t.Fatal(err)
		}
		c.Ready.Settle()
	}
	got := operatorRead(t, c.Service)
	if !got.At.Equal(boardAt) || len(got.Hosts) != 1 {
		t.Fatalf("fleet: %+v", got)
	}
	row := got.Hosts[0]
	if row.Name != "devbox" || row.Slots != 2 || row.HeavySlots != 1 || !reflect.DeepEqual(row.SignedOut, []string{"codex"}) {
		t.Fatalf("host: %+v", row)
	}
	if want := []control.HostQuota{{Harness: "claude", Window: "5h", Used: 40, ResetsAt: 1700000000}}; !reflect.DeepEqual(row.Quota, want) {
		t.Fatalf("quota: %+v", row.Quota)
	}
	if want := []control.HostReadiness{{Repo: "owner/demo", State: "ready"}}; !reflect.DeepEqual(row.Readiness, want) {
		t.Fatalf("readiness: %+v", row.Readiness)
	}
	if len(row.Windows) != 1 || row.Windows[0].Item != task.ID || row.Windows[0].Attempt == "" || row.Windows[0].State != session.StateWorking || !row.Windows[0].Alive {
		t.Fatalf("windows: %+v", row.Windows)
	}
}

func TestOnlyTheOperatorReadsTheFleet(t *testing.T) {
	c, _, _, _, _ := setup(t)
	if _, err := c.Service.Execute(control.Actor{Name: "w", Role: "worker"}, control.Request{Op: "host-status"}); err == nil {
		t.Fatal("a worker read the fleet")
	}
	// A service with no board reports no hosts, not an error.
	if got := operatorRead(t, c.Service); got.Hosts == nil || len(got.Hosts) != 0 {
		t.Fatalf("no board: %+v", got)
	}
}

func TestTheBoardShowsAnUnreachableHostAndKeepsOnlyTheLatestPass(t *testing.T) {
	var b *HostBoard
	b.Record(control.State{}, reconcile.Fleet{}, nil) // a nil board records nothing
	b = &HostBoard{Now: func() time.Time { return boardAt }}
	down := reconcile.Load("mac", nil, nil, hostops.Status{}, errString("ssh: timed out"))
	up := reconcile.Load("devbox", nil, nil, hostops.Status{Slots: 2, FreeSlots: 1, Windows: []hostops.Window{{Name: "demo-9", ID: "@2"}}}, nil)
	b.Record(control.State{}, reconcile.Fleet{Hosts: []reconcile.HostLoad{down, up}}, nil)
	got := b.Status()
	if len(got.Hosts) != 2 || got.Hosts[0].Unreachable != "ssh: timed out" || got.Hosts[0].Windows == nil || got.Hosts[0].Slots != 0 {
		t.Fatalf("unreachable host: %+v", got.Hosts)
	}
	if w := got.Hosts[1].Windows; len(w) != 1 || w[0].Item != "" || w[0].Alive {
		t.Fatalf("a window no attempt runs: %+v", w)
	}
	b.Record(control.State{}, reconcile.Fleet{Hosts: []reconcile.HostLoad{up}}, nil)
	if got = b.Status(); len(got.Hosts) != 1 || got.Hosts[0].Name != "devbox" {
		t.Fatalf("a host that left hosts.toml stays: %+v", got.Hosts)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestReadinessReportNamesEachRepo(t *testing.T) {
	r := &Readiness{entries: map[readyAt]*readyEntry{
		{"devbox", "r1"}: {slug: "o/a", result: &hostops.Readiness{Checks: []hostops.ReadyCheck{{Name: "gh auth status", OK: true}}}},
		{"devbox", "r2"}: {slug: "o/b", result: &hostops.Readiness{Checks: []hostops.ReadyCheck{{Name: "gh auth status", OK: false, Output: "refused"}}}},
		{"devbox", "r3"}: {slug: "o/c", err: "ssh: timed out"},
		{"devbox", "r4"}: {slug: "o/d"},
		{"mac", "r1"}:    {slug: "o/a"},
	}}
	want := []control.HostReadiness{
		{Repo: "o/a", State: "ready"},
		{Repo: "o/b", State: "unready", Detail: "gh auth status failed (the readiness note has the output)"},
		{Repo: "o/c", State: "unready", Detail: "its readiness probe could not run: ssh: timed out"},
		{Repo: "o/d", State: "unknown"},
	}
	if got := r.Report("devbox"); !reflect.DeepEqual(got, want) {
		t.Fatalf("report: %+v", got)
	}
}
