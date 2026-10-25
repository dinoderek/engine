package start

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// A card body whose Harness / tier section names the given words.
func card(tier string) string { return "## Outcome\n\nx\n\n## Harness / tier\n\n" + tier + "\n" }

// refusal is one way engine start must refuse. Empty fields take the
// defaults of a task that would start: host mac, an open sub-task at deep.
type refusal struct {
	name    string
	host    string
	opts    Options
	issue   issue
	windows []string
	env     map[string]string
	setup   func(t *testing.T, h home)
	want    string
}

var startable = issue{number: 7, state: "open", parent: 3, body: card("deep")}

func TestRefusalsChangeNothing(t *testing.T) {
	with := func(edit func(*issue)) issue { i := startable; edit(&i); return i }
	cases := []refusal{
		{name: "unknown mode", opts: Options{Mode: "yolo"}, want: `unknown mode "yolo"`},
		{name: "unknown tier", opts: Options{Tier: "deeep"}, want: `unknown tier "deeep"`},
		{name: "no token", setup: removeFile(".config/engine/github-token"), want: "the session reads the machine user's token"},
		{name: "GH_TOKEN but no token file", env: map[string]string{"GH_TOKEN": "tok"},
			setup: removeFile(".config/engine/github-token"), want: "the session reads the machine user's token"},
		{name: "empty token file, GH_TOKEN set", env: map[string]string{"GH_TOKEN": "tok"},
			setup: emptyFile(".config/engine/github-token"), want: "is empty"},
		{name: "no host file", setup: removeFile(".config/engine/host"), want: "read host file"},
		{name: "no hosts.toml", setup: removeFile(".config/engine/hosts.toml"), want: "read host config"},
		{name: "host not in hosts.toml", host: "laptop", want: `host "laptop" is not in hosts.toml`},
		{name: "closed", issue: with(func(i *issue) { i.state = "closed" }), want: "is closed"},
		{name: "running", issue: with(func(i *issue) { i.labels = []string{"engine:running"} }), want: "is engine:running"},
		{name: "needs-human", issue: with(func(i *issue) { i.labels = []string{"engine:needs-human"} }), want: "is engine:needs-human"},
		{name: "in review", issue: with(func(i *issue) { i.labels = []string{"engine:in-review"} }), want: "is engine:in-review"},
		{name: "failed", issue: with(func(i *issue) { i.labels = []string{"engine:failed"} }), want: "is engine:failed"},
		{name: "standalone, not approved", issue: with(func(i *issue) { i.parent, i.labels = 0, []string{"engine:prepare"} }),
			want: "standalone task without engine:approved"},
		{name: "open blocker", issue: with(func(i *issue) { i.blockers = []int{5} }), want: "is blocked by open o/r#"},
		{name: "misspelt tier on the card", issue: with(func(i *issue) { i.body = card("deeep") }),
			want: `harness "deeep", which hosts.toml does not define (a misspelt tier?`},
		{name: "codex", issue: with(func(i *issue) { i.body = card("codex, light") }), want: "engine start launches claude only"},
		{name: "host does not run claude", host: "box", want: "host box does not run claude"},
		{name: "no harness binary", setup: removeFile("bin/claude"), want: "is not an executable claude binary"},
		{name: "host lacks a required capability", issue: with(func(i *issue) { i.body = "## Requires\nios, macos\n\n" + card("deep") }),
			want: "host mac lacks ios, which " + ref(7).String() + " requires (it has: macos)"},
		{name: "named host lacks it too", opts: Options{Host: "mac"}, issue: with(func(i *issue) { i.body = "## Requires\nlinux\n\n" + card("deep") }),
			want: "host mac lacks linux"},
		{name: "a word no host declares", issue: with(func(i *issue) { i.body = "## Requires\nteleporter\n\n" + card("deep") }),
			want: "host mac lacks teleporter"},
		{name: "unknown --host", opts: Options{Host: "nowhere"}, want: `host "nowhere" is not in hosts.toml; its machines are box, dev, mac`},
		{name: "window exists", windows: []string{"@1 %1 0 r-7"}, want: "already has a window r-7 (@1)"},
		{name: "no free slot", windows: []string{"@1 %1 0 r-1", "@2 %2 0 r-2"}, setup: withRuns("r-1", "r-2"),
			want: "no free slot: 2 sessions running, 2 slots"},
		{name: "a stray window and a session fill the slots with another", windows: []string{"@1 %1 0 r-1", "@2 %2 0 zsh", "@3 %3 0 r-3"},
			setup: withRuns("r-1", "r-3"), want: "no free slot: 2 sessions running, 2 slots"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkRefusal(t, c) })
	}
}

// checkRefusal runs engine start and wants a refusal that wrote no file, ran
// no git, and only read the tracker and tmux.
func checkRefusal(t *testing.T, c refusal) {
	if c.host == "" {
		c.host = "mac"
	}
	if c.issue.number == 0 {
		c.issue = startable
	}
	if c.opts.Ref.Repo == "" {
		c.opts.Ref = ref(7)
	}
	h := newHome(t, c.host)
	if c.setup != nil {
		c.setup(t, h)
	}
	before := files(t, h.dir)
	gh := &ghFake{issue: c.issue}
	tm := &tmuxFake{windows: c.windows}
	d := Deps{GH: gh.run, Run: gitFake(t), Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir,
		Getenv: func(k string) string { return c.env[k] }, Now: fixedNow}
	var out bytes.Buffer
	err := Run(c.opts, d, &out)
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) {
		t.Fatalf("err = %v, want a refusal containing %q", err, c.want)
	}
	if !strings.HasSuffix(err.Error(), "nothing was changed") || out.Len() > 0 {
		t.Errorf("a refusal says nothing changed and prints nothing: %q, %q", err, out.String())
	}
	if after := files(t, h.dir); !slices.Equal(before, after) {
		t.Errorf("files changed:\nbefore %v\nafter  %v", before, after)
	}
	gh.readOnly(t)
	tm.readOnly(t)
}

func emptyFile(rel string) func(*testing.T, home) {
	return func(t *testing.T, h home) {
		t.Helper()
		write(t, filepath.Join(h.dir, rel), " \n", 0o600)
	}
}

func removeFile(rel string) func(*testing.T, home) {
	return func(t *testing.T, h home) {
		t.Helper()
		if err := os.Remove(filepath.Join(h.dir, rel)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOnlySessionsHoldSlots(t *testing.T) {
	// Two slots. Two dead sessions, a live shell, and engine run's own window
	// hold none, so a start is not refused.
	h := newHome(t, "mac")
	writeRuns(t, h, "r-1", "r-2")
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3}}
	tm := &tmuxFake{windows: []string{"@1 %1 1 r-1", "@2 %2 1 r-2", "@3 %3 0 zsh", "@4 %4 0 run-o-3"}}
	d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
	if _, err := check(Options{Ref: ref(7)}, d); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadableRunRecordRefusesStart(t *testing.T) {
	h := newHome(t, "mac")
	writeRuns(t, h, "r-1")
	dir, err := session.RunDir(h.dir, "r-9-a")
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, session.RecordFile), []byte("{"), 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3}}
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}}
	d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
	if _, err := check(Options{Ref: ref(7)}, d); err == nil || !strings.Contains(err.Error(), "cannot read the run records") {
		t.Errorf("err = %v", err)
	}
}

func TestDeadWindowsHoldNoSlot(t *testing.T) {
	h := newHome(t, "mac")
	writeRuns(t, h, "r-1", "r-2", "r-3")
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3}}
	tm := &tmuxFake{windows: []string{"@1 %1 1 r-1", "@2 %2 1 r-2", "@3 %3 0 r-3"}}
	d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
	p, err := check(Options{Ref: ref(7)}, d)
	if err != nil {
		t.Fatal(err)
	}
	if p.tier != "standard" || p.model.Model != "sonnet-x" || p.reviewer != "opus-x" || p.opts.Mode != HandsOff {
		t.Errorf("an empty card runs the default harness at standard, hands-off: %+v", p)
	}
}

func TestTierFlagReplacesTheCard(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: issue{number: 7, state: "open", labels: []string{"engine:approved"}, body: card("deep")}}
	tm := &tmuxFake{}
	d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
	p, err := check(Options{Ref: ref(7), Tier: "light", Mode: Gated}, d)
	if err != nil {
		t.Fatal(err)
	}
	if p.tier != "light" || p.model.Model != "haiku-x" || p.reviewer != "opus-x" || p.opts.Mode != Gated {
		t.Errorf("--tier light, gated, on an approved standalone task: %+v", p)
	}
}

func TestSpawnRequest(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3, body: card("deep")}}
	for _, mode := range []string{HandsOff, Gated} {
		d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: (&tmuxFake{}).run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
		p, err := check(Options{Ref: ref(7), Mode: mode}, d)
		if err != nil {
			t.Fatal(err)
		}
		// The caller decides the model and the kickoff; the host only
		// launches them.
		want := hostops.SpawnRequest{Repo: "o/r", Task: 7, Feature: ref(3).String(), Mode: mode, Tier: "deep",
			Model: "opus-x", Effort: "high", RunID: "r-7-20260102T030405Z",
			Kickoff: "Execute " + ref(7).String() + " with the engine executor, " + mode + " mode. Reviewer model: opus-x. run=r-7-20260102T030405Z tmux=engine:r-7 rc=r-7"}
		if got := p.spawnRequest("r-7-20260102T030405Z"); got != want {
			t.Errorf("spawn request\n got %+v\nwant %+v", got, want)
		}
	}
}

// clientFake is a host that records what engine start asks of it.
type clientFake struct {
	remote.Client
	synced  []string
	spawned []hostops.SpawnRequest
}

func (f *clientFake) SyncRepo(repo string) (hostops.Synced, error) {
	f.synced = append(f.synced, repo)
	return hostops.Synced{}, nil
}

func (f *clientFake) Spawn(r hostops.SpawnRequest) (hostops.Spawned, error) {
	f.spawned = append(f.spawned, r)
	return hostops.Spawned{Name: "r-7", Window: "@4", RunDir: "/runs/x", Worktree: "/wt", Branch: "task/r-7", Attach: "ssh -t me@box tmux attach"}, nil
}

// With Deps.Client set, the session starts on that host: engine start syncs
// and spawns there, and runs nothing here.
func TestStartGoesThroughTheClient(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: startable}
	tm := &tmuxFake{}
	c := &clientFake{}
	d := Deps{GH: gh.run, Run: gitFake(t), Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir,
		Getenv: func(string) string { return "" }, Now: fixedNow, Client: c}
	var out bytes.Buffer
	if err := Run(Options{Ref: ref(7)}, d, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.synced, []string{"o/r"}) || len(c.spawned) != 1 || c.spawned[0].RunID != "r-7-20260102T030405Z" || c.spawned[0].Model != "opus-x" {
		t.Errorf("synced %v, spawned %+v", c.synced, c.spawned)
	}
	if !strings.Contains(out.String(), "tmux window     engine:@4 (r-7)") || !strings.Contains(out.String(), "or: ssh -t me@box tmux attach") {
		t.Errorf("report:\n%s", out.String())
	}
	tm.readOnly(t)
}
