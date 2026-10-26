package start

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// hostFake is another machine, as engine start sees it: it answers status
// from a canned reply and records every call.
type hostFake struct {
	remote.Client
	status    hostops.Status
	statusErr error
	calls     []string
	spawned   []hostops.SpawnRequest
	checked   bool
	// checkedHarness is the harness the last check named.
	checkedHarness string
	checkOK        bool
	checkErr       error
}

func (f *hostFake) Status() (hostops.Status, error) {
	f.calls = append(f.calls, "status")
	return f.status, f.statusErr
}

func (f *hostFake) SyncRepo(repo string) (hostops.Synced, error) {
	f.calls = append(f.calls, "sync-repo "+repo)
	return hostops.Synced{}, nil
}

func (f *hostFake) Spawn(r hostops.SpawnRequest) (hostops.Spawned, error) {
	f.calls = append(f.calls, "spawn "+r.RunID)
	f.spawned = append(f.spawned, r)
	return hostops.Spawned{Name: "r-7", Window: "@4", RunDir: "/runs/x", Worktree: "/wt", Branch: "task/r-7",
		Attach: "tmux attach -t '=engine:@4'"}, nil
}

func (f *hostFake) Check(harness string, out io.Writer) (bool, error) {
	f.checked, f.checkedHarness = true, harness
	_, _ = io.WriteString(out, "the report\n")
	return f.checkOK, f.checkErr
}

// remoteDeps are Deps whose named machine is f, with no local tmux or git to
// reach: starting elsewhere changes nothing here.
func remoteDeps(t *testing.T, h home, gh *ghFake, f remote.Client) Deps {
	t.Helper()
	return Deps{GH: gh.run, Run: gitFake(t), Tmux: &session.Tmux{Run: (&tmuxFake{}).run, Bin: "tmux"}, Home: h.dir,
		Getenv: func(string) string { return "" }, Now: fixedNow, Client: f}
}

func TestStartOnAnotherHost(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3, body: "## Requires\nlinux\n\n" + card("deep")}}
	f := &hostFake{status: hostops.Status{Slots: 3, FreeSlots: 1, Windows: []hostops.Window{{Name: "r-1", ID: "@1", Alive: true}}}}
	var out bytes.Buffer
	if err := Run(Options{Ref: ref(7), Host: "dev"}, remoteDeps(t, h, gh, f), &out); err != nil {
		t.Fatal(err)
	}
	if want := []string{"status", "sync-repo o/r", "spawn r-7-20260102T030405Z"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls %v, want %v", f.calls, want)
	}
	if r := f.spawned[0]; r.Model != "opus-x" || r.Effort != "high" || r.Tier != "deep" || r.Feature != ref(3).String() || r.Mode != HandsOff {
		t.Errorf("spawn request %+v", r)
	}
	for _, want := range []string{"Started " + ref(7).String() + " as r-7 on dev.", "tmux window     engine:@4 (r-7)", "Attach: engine attach r-7",
		`or: ssh -t me@dev 'tmux attach -t '\''=engine:@4'\'''`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

// Another machine's refusals are the caller's, from its status, and nothing
// reaches the host but that one read.
func TestRefusalsOnAnotherHostChangeNothing(t *testing.T) {
	with := func(edit func(*issue)) issue { i := startable; edit(&i); return i }
	cases := []struct {
		name   string
		issue  issue
		status hostops.Status
		want   string
	}{
		{"window exists", startable, hostops.Status{Slots: 3, FreeSlots: 2, Windows: []hostops.Window{{Name: "r-7", ID: "@9"}}},
			"host dev: the tmux session engine already has a window r-7 (@9)"},
		{"no free slot", startable, hostops.Status{Slots: 3}, "host dev has no free slot: 3 slots"},
		{"lacks a capability", with(func(i *issue) { i.body = "## Requires\nios\n\n" + card("deep") }), hostops.Status{Slots: 3, FreeSlots: 3},
			"host dev lacks ios, which " + ref(7).String() + " requires (it has: linux, docker)"},
		{"closed", with(func(i *issue) { i.state = "closed" }), hostops.Status{FreeSlots: 3}, "is closed"},
		{"running", with(func(i *issue) { i.labels = []string{"engine:running"} }), hostops.Status{FreeSlots: 3}, "is engine:running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHome(t, "mac")
			f := &hostFake{status: tc.status}
			err := Run(Options{Ref: ref(7), Host: "dev"}, remoteDeps(t, h, &ghFake{issue: tc.issue}, f), &bytes.Buffer{})
			var refused *RefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), tc.want) || !strings.HasSuffix(err.Error(), "; nothing was changed") {
				t.Fatalf("err = %v, want a refusal containing %q", err, tc.want)
			}
			if len(f.calls) > 1 || len(f.calls) == 1 && f.calls[0] != "status" {
				t.Errorf("a refusal reached the host beyond status: %v", f.calls)
			}
		})
	}
}

// A host that cannot be read is not a refusal, and starts nothing.
func TestStartOnAnUnreachableHost(t *testing.T) {
	h := newHome(t, "mac")
	f := &hostFake{statusErr: &remote.UnreachableError{Host: "dev", Address: "me@dev", Detail: "no route"}}
	err := Run(Options{Ref: ref(7), Host: "dev"}, remoteDeps(t, h, &ghFake{issue: startable}, f), &bytes.Buffer{})
	var unreachable *remote.UnreachableError
	var refused *RefusedError
	if !errors.As(err, &unreachable) || errors.As(err, &refused) || !slices.Equal(f.calls, []string{"status"}) {
		t.Errorf("err = %v, calls %v", err, f.calls)
	}
}

// Naming this host starts here, with the local checks.
func TestStartHostThatIsThisOne(t *testing.T) {
	h := newHome(t, "mac")
	f := &hostFake{}
	d := remoteDeps(t, h, &ghFake{issue: startable}, f)
	writeRuns(t, h, "r-1", "r-2")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1", "@2 %2 0 r-2"}}
	d.Tmux = &session.Tmux{Run: tm.run, Bin: "tmux"}
	err := Run(Options{Ref: ref(7), Host: "mac"}, d, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no free slot: 2 sessions running, 2 slots") || len(f.calls) != 0 {
		t.Errorf("err = %v, calls %v", err, f.calls)
	}
}

func TestCheckOnAHost(t *testing.T) {
	h := newHome(t, "mac")
	f := &hostFake{checkOK: true}
	d := remoteDeps(t, h, &ghFake{}, f)
	var out bytes.Buffer
	if ok, err := Check(d, "dev", "", &out); !ok || err != nil || !f.checked || f.checkedHarness != "" || out.String() != "the report\n" {
		t.Errorf("ok=%v err=%v checked=%v out=%q", ok, err, f.checked, out.String())
	}
	f = &hostFake{checkOK: false}
	if ok, err := Check(remoteDeps(t, h, &ghFake{}, f), "dev", "codex", &out); ok || err != nil || !f.checked || f.checkedHarness != "codex" {
		t.Errorf("a failed check of codex: ok=%v err=%v harness=%q", ok, err, f.checkedHarness)
	}
	f = &hostFake{checkErr: errors.New("unreachable")}
	if ok, err := Check(remoteDeps(t, h, &ghFake{}, f), "", "", &out); ok || err == nil || !f.checked {
		t.Errorf("no host: ok=%v err=%v checked=%v", ok, err, f.checked)
	}
}

func TestCheckRefusesWhatItCannotReach(t *testing.T) {
	h := newHome(t, "mac")
	d := remoteDeps(t, h, &ghFake{}, &hostFake{})
	_, err := Check(d, "nowhere", "", &bytes.Buffer{})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), `host "nowhere" is not in hosts.toml`) {
		t.Errorf("unknown host: %v", err)
	}
	d.Home = t.TempDir()
	if _, err := Check(d, "dev", "", &bytes.Buffer{}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "read host config") {
		t.Errorf("no host config: %v", err)
	}
}

// With --host, the caller refuses a harness the host does not run before it
// reaches the host, and sends one it does run in the spawn.
func TestStartOnAnotherHostWithAHarness(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3, body: card("deep")}}
	f := &hostFake{status: hostops.Status{Slots: 3, FreeSlots: 1}}
	err := Run(Options{Ref: ref(7), Host: "dev", Harness: "codex"}, remoteDeps(t, h, gh, f), &bytes.Buffer{})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "host dev does not run codex") || len(f.calls) != 0 {
		t.Errorf("err = %v, calls %v", err, f.calls)
	}
	if err := Run(Options{Ref: ref(7), Host: "box", Harness: "codex"}, remoteDeps(t, h, gh, f), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(f.spawned) != 1 || f.spawned[0].Harness != "codex" || f.spawned[0].Model != "sol" {
		t.Errorf("spawned %+v", f.spawned)
	}
}
