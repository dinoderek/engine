package start

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hosts"
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
		{name: "window exists", windows: []string{"@1 %1 0 r-7"}, want: "already has a window r-7 (@1)"},
		{name: "no free slot", windows: []string{"@1 %1 0 r-1", "@2 %2 0 r-2"}, want: "no free slot: 2 sessions running, 2 slots"},
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

func TestDeadWindowsHoldNoSlot(t *testing.T) {
	h := newHome(t, "mac")
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

func TestLaunchCommand(t *testing.T) {
	h := newHome(t, "mac")
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3, body: card("deep")}}
	for _, configDir := range []string{"", "/cfg dir"} {
		env := func(k string) string { return map[string]string{"CLAUDE_CONFIG_DIR": configDir}[k] }
		d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: (&tmuxFake{}).run, Bin: "tmux"}, Home: h.dir, Getenv: env}
		p, err := check(Options{Ref: ref(7)}, d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.command(d, session.Clone{Path: "/clones/o/r"}, "r-7-20260102T030405Z")
		if err != nil {
			t.Fatal(err)
		}
		script := `GH_TOKEN="$(cat ` + filepath.Join(h.dir, ".config/engine/github-token") + `)" && [ -n "$GH_TOKEN" ] && export GH_TOKEN && `
		plugin := filepath.Join(h.dir, ".claude/plugins/cache/engine")
		if configDir != "" {
			script += `export CLAUDE_CONFIG_DIR='/cfg dir' && `
			plugin = `'/cfg dir/plugins/cache/engine'`
		} else {
			script += `unset CLAUDE_CONFIG_DIR && `
		}
		// The skill's directory is a working directory, /tmp is readable
		// only, and -- keeps the variadic options from taking the kickoff.
		script += `exec ` + h.bin + ` --permission-mode auto --model opus-x --effort high -n r-7 --remote-control r-7 ` +
			`--add-dir ` + plugin + ` --allowedTools 'Read(//tmp/**)' 'Read(//private/tmp/**)' -- ` +
			`'Execute ` + ref(7).String() + ` with the engine executor, hands-off mode. Reviewer model: opus-x. run=r-7-20260102T030405Z tmux=engine:r-7 rc=r-7'`
		if want := "exec /bin/sh -c " + hosts.ShellWord(script); got != want {
			t.Errorf("command\n got %s\nwant %s", got, want)
		}
	}
}
