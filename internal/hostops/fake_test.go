package hostops

import (
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/session"
)

// The ops ask only the interface: a spawn trusts the folder and runs the
// adapter's environment and arguments after the launch line, and the run
// records the adapter's name.
func TestSpawnLaunchesThroughTheAdapter(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	tm := &tmuxFake{}
	fake := &harnesstest.Fake{Called: Claude, Joined: false, Warning: "no daemon here"}
	d := spawnDeps(t, h, (&gitFake{}).run, tm, nil)
	d.Harnesses, d.JoinRemote = harness.NewAdapters(fake), true
	got, err := Spawn(d, request())
	if err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "join r-7 ") || calls[1] != "trust r-7 "+got.Worktree {
		t.Errorf("calls %v", calls)
	}
	if !strings.Contains(tm.command, "export FAKE_HOME=/fake && exec ") || !strings.Contains(tm.command, " fake-args -- ") {
		t.Errorf("command %s", tm.command)
	}
	if got.Warning != "no daemon here" {
		t.Errorf("warning %q", got.Warning)
	}
	rec, err := session.ReadRecord(got.RunDir)
	if err != nil || rec.Harness != Claude {
		t.Errorf("run.json %+v, %v", rec, err)
	}
	d.Harnesses = harness.NewAdapters(&harnesstest.Fake{Called: "other"})
	req := request()
	req.Harness, req.RunID = Codex, "r-7-again"
	if _, err := Spawn(d, req); !isRefusal(err) || !strings.Contains(err.Error(), `unknown harness "codex": want other`) {
		t.Errorf("unknown harness: %v", err)
	}
}

// Status hands each adapter the newest run of every live window that names
// it, and reports what each says: its quota, sign-in, errors and states.
func TestStatusReportsEachAdaptersSnapshot(t *testing.T) {
	h := newHome(t, "mac")
	at := fixedNow()
	writeRun(t, h, session.Record{RunID: "a", Name: "w-a", Harness: "alpha", Spawned: at})
	writeRun(t, h, session.Record{RunID: "b", Name: "w-b", Harness: "beta", Spawned: at})
	writeRun(t, h, session.Record{RunID: "c", Name: "w-c", Harness: "alpha", Spawned: at})
	tm := &tmuxFake{windows: []string{"@1 %1 0 w-a", "@2 %2 0 w-b", "@3 %3 1 w-c"}, screens: map[string]string{"@2": "waiting"}}
	q := session.Quota{Harness: "alpha", ObservedAt: at.Unix(), Windows: map[string]session.QuotaWindow{"five_hour": {Used: 10, ResetsAt: at.Unix() + 60}}}
	alpha := &harnesstest.Fake{Called: "alpha", Snapshot: harness.Snapshot{Quota: &q, SignedOut: true,
		Sessions: map[string]harness.State{"w-a": {State: session.StateWorking, Background: true}, "w-c": {State: session.StateWorking}}}}
	beta := &harnesstest.Fake{Called: "beta", Snapshot: harness.Snapshot{Error: "beta cannot list its sessions"}}
	got, err := ReadStatus(Deps{Home: h.dir, Tmux: tm.tmux(), Harnesses: harness.NewAdapters(alpha, beta)})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha.Observed) != 1 || len(alpha.Observed[0]) != 1 || alpha.Observed[0]["w-a"].Record.RunID != "a" {
		t.Errorf("alpha observed %v", alpha.Observed)
	}
	if len(beta.Observed) != 1 || len(beta.Observed[0]) != 1 {
		t.Errorf("beta observed %v", beta.Observed)
	}
	if got.Quota["alpha"].Windows["five_hour"].Used != 10 || len(got.Quota) != 1 {
		t.Errorf("quota %+v", got.Quota)
	}
	if len(got.SignedOut) != 1 || got.SignedOut[0] != "alpha" || got.HarnessErrors["beta"] != "beta cannot list its sessions" {
		t.Errorf("signed out %v, errors %v", got.SignedOut, got.HarnessErrors)
	}
	a, b, c := got.Windows[0], got.Windows[1], got.Windows[2]
	if a.State != session.StateWorking || !a.Background || a.Screen != "" {
		t.Errorf("w-a %+v", a)
	}
	if b.State != "" || b.Screen != "waiting" {
		t.Errorf("w-b %+v", b)
	}
	if c.Alive || c.State != "" {
		t.Errorf("a dead window has a state: %+v", c)
	}
}

// Release asks every adapter, whatever the run's harness, and an adapter
// that cannot delete its files leaves a note, not a failure.
func TestReleaseAsksEveryAdapter(t *testing.T) {
	h := newHome(t, "mac")
	writeRun(t, h, session.Record{RunID: "r-7-a", Name: "r-7", Harness: "alpha", Spawned: fixedNow()})
	alpha := &harnesstest.Fake{Called: "alpha"}
	beta := &harnesstest.Fake{Called: "beta", ReleaseErr: errors.New("beta's profile is stuck")}
	r, err := Release(Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Harnesses: harness.NewAdapters(alpha, beta)}, "r-7", RunEnded)
	if err != nil || r.Note != "beta's profile is stuck" {
		t.Fatalf("%+v, %v", r, err)
	}
	if a, b := alpha.Calls(), beta.Calls(); len(a) != 1 || a[0] != "release r-7 " || len(b) != 1 {
		t.Errorf("alpha %v, beta %v", a, b)
	}
}

// An op handed no adapters refuses, so a caller that forgets them hears of it.
func TestOpsRefuseWithoutAdapters(t *testing.T) {
	h := newHome(t, "mac")
	none := harness.NewAdapters()
	d := Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Harnesses: none}
	if _, err := ReadStatus(d); !isRefusal(err) || !strings.Contains(err.Error(), "no harness adapters") {
		t.Errorf("status: %v", err)
	}
	if _, err := Release(d, "r-7", RunEnded); !isRefusal(err) {
		t.Errorf("release: %v", err)
	}
	if _, err := Spawn(d, request()); !isRefusal(err) || !strings.Contains(err.Error(), "no harness adapters") {
		t.Errorf("spawn: %v", err)
	}
}
