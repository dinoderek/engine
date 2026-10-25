package hostops

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// ref is issue n of o/r, as owner/repo#n.
func ref(n int) string { return tracker.Ref{Repo: "o/r", Number: n}.String() }

// request is a spawn that would launch: task 7 of o/r, hands-off.
func request() SpawnRequest {
	return SpawnRequest{Repo: "o/r", Task: 7, Feature: ref(3), Mode: HandsOff, Tier: "deep", Model: "opus-x", Effort: "high",
		RunID: "r-7-20260102T030405Z", Kickoff: "Execute " + ref(7) + " with the engine executor, hands-off mode."}
}

func spawnDeps(t *testing.T, h home, git session.RunFunc, tm *tmuxFake, env map[string]string) Deps {
	t.Helper()
	return Deps{Run: git, Tmux: tm.tmux(), Home: h.dir, Getenv: func(k string) string { return env[k] }, Now: fixedNow}
}

func TestSpawn(t *testing.T) {
	h := newHome(t, "mac")
	clone := h.clone(t)
	git := &gitFake{}
	tm := &tmuxFake{windows: []string{"@1 %1 0 other"}}
	got, err := Spawn(spawnDeps(t, h, git.run, tm, nil), request())
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(h.dir, ".local/share/engine/runs/r-7-20260102T030405Z")
	worktree := clone + "-worktrees/task-r-7"
	want := Spawned{Name: "r-7", Window: "@9", RunDir: runDir, Worktree: worktree, Branch: "task/r-7", Attach: "tmux attach -t '=engine:@9'"}
	if got != want {
		t.Errorf("reply\n got %+v\nwant %+v", got, want)
	}
	// Spawn opens the clone sync-repo left; it never fetches.
	if slices.ContainsFunc(git.calls, func(c string) bool { return strings.Contains(c, "fetch") }) {
		t.Errorf("spawn fetched: %v", git.calls)
	}
	var rec session.Record
	data, err := os.ReadFile(filepath.Join(runDir, session.RecordFile))
	if err != nil || json.Unmarshal(data, &rec) != nil {
		t.Fatalf("run.json: %s, %v", data, err)
	}
	wantRec := session.Record{Schema: 1, RunID: "r-7-20260102T030405Z", Repo: "o/r", Task: ref(7), Feature: ref(3), Host: "mac",
		Harness: "claude", Tier: "deep", Model: "opus-x", Effort: "high", Mode: HandsOff, Name: "r-7", Tmux: "engine:@9",
		Branch: "task/r-7", Worktree: worktree, PaneLog: filepath.Join(runDir, session.PaneLog), Spawned: fixedNow()}
	if rec != wantRec {
		t.Errorf("run.json\n got %+v\nwant %+v", rec, wantRec)
	}
	if trust, _ := os.ReadFile(filepath.Join(h.dir, ".claude.json")); !strings.Contains(string(trust), worktree) {
		t.Errorf("the worktree is not trusted: %s", trust)
	}
}

// TestLaunchCommand pins the window's command: the token from its file, the
// run directory, the unattended mark in hands-off mode only, the Claude
// config directory, the caller's model, and the kickoff after --.
func TestLaunchCommand(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg dir")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ configDir, mode string }{{"", HandsOff}, {cfg, HandsOff}, {"", Gated}} {
		h := newHome(t, "mac")
		h.clone(t)
		tm := &tmuxFake{}
		r := request()
		r.Mode, r.Model = c.mode, "opus-y"
		if _, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, tm, map[string]string{"CLAUDE_CONFIG_DIR": c.configDir}), r); err != nil {
			t.Fatal(err)
		}
		script := `GH_TOKEN="$(cat ` + filepath.Join(h.dir, ".config/engine/github-token") + `)" && [ -n "$GH_TOKEN" ] && export GH_TOKEN && `
		script += `export ENGINE_RUN_DIR=` + filepath.Join(h.dir, ".local/share/engine/runs/r-7-20260102T030405Z") + ` && `
		if c.mode == HandsOff {
			script += `export ENGINE_UNATTENDED=1 && `
		} else {
			script += `unset ENGINE_UNATTENDED && `
		}
		plugin := filepath.Join(h.dir, ".claude/plugins/cache/engine")
		if c.configDir != "" {
			script += `export CLAUDE_CONFIG_DIR=` + hosts.ShellWord(cfg) + ` && `
			plugin = hosts.ShellWord(filepath.Join(cfg, "plugins/cache/engine"))
		} else {
			script += `unset CLAUDE_CONFIG_DIR && `
		}
		// The skill's directory is a working directory, /tmp is readable
		// only, and -- keeps the variadic options from taking the kickoff.
		script += `exec ` + h.bin + ` --permission-mode auto --model opus-y --effort high -n r-7 --remote-control r-7 ` +
			`--add-dir ` + plugin + ` --allowedTools 'Read(//tmp/**)' 'Read(//private/tmp/**)' -- ` +
			`'Execute ` + ref(7) + ` with the engine executor, hands-off mode.'`
		if want := "exec /bin/sh -c " + hosts.ShellWord(script); tm.command != want {
			t.Errorf("command\n got %s\nwant %s", tm.command, want)
		}
	}
}

func TestSpawnRefusalsChangeNothing(t *testing.T) {
	with := func(edit func(*SpawnRequest)) SpawnRequest { r := request(); edit(&r); return r }
	cases := []struct {
		name    string
		host    string
		req     SpawnRequest
		windows []string
		setup   func(t *testing.T, h home)
		want    string
	}{
		{name: "bad repo", req: with(func(r *SpawnRequest) { r.Repo = "o" }), want: `want owner/repo, got "o"`},
		{name: "everything else wrong", req: SpawnRequest{Repo: "o/r", Tier: "huge", Feature: "x", RunID: "../x"},
			want: `task number must be positive, got 0; unknown mode "": want hands-off or gated; a model and an effort are required; ` +
				`the kickoff is empty; unknown tier "huge": want deep, standard or light; feature: want owner/repo#n, got "x"; run id "../x"`},
		{name: "bad feature", req: with(func(r *SpawnRequest) { r.Feature = "o/r" }), want: "feature: "},
		{name: "bad run id", req: with(func(r *SpawnRequest) { r.RunID = "a/b" }), want: `run id "a/b"`},
		{name: "no token", setup: remove(".config/engine/github-token"), want: "the session reads the machine user's token"},
		{name: "no hosts.toml", setup: remove(".config/engine/hosts.toml"), want: "read host config"},
		{name: "host does not run claude", host: "box", want: "host box does not run claude"},
		{name: "no harness binary", setup: remove("bin/claude"), want: "is not an executable claude binary"},
		{name: "window exists", windows: []string{"@1 %1 0 r-7"}, want: "already has a window r-7"},
		{name: "no clone", setup: func(t *testing.T, h home) {
			if err := os.RemoveAll(filepath.Join(h.dir, ".local")); err != nil {
				t.Fatal(err)
			}
		}, want: "sync it first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.host == "" {
				c.host = "mac"
			}
			if c.req.Repo == "" {
				c.req = request()
			}
			h := newHome(t, c.host)
			h.clone(t)
			if c.setup != nil {
				c.setup(t, h)
			}
			before := files(t, h.dir)
			tm := &tmuxFake{windows: c.windows}
			git := &gitFake{}
			_, err := Spawn(spawnDeps(t, h, git.run, tm, nil), c.req)
			var refused *RefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) || !strings.HasSuffix(err.Error(), "nothing was changed") {
				t.Fatalf("err = %v, want a refusal containing %q", err, c.want)
			}
			if after := files(t, h.dir); !slices.Equal(before, after) {
				t.Errorf("files changed:\nbefore %v\nafter  %v", before, after)
			}
			if slices.ContainsFunc(git.calls, func(c string) bool { return !strings.Contains(c, "symbolic-ref") }) {
				t.Errorf("a refusal ran %v", git.calls)
			}
			tm.readOnly(t)
		})
	}
}

func remove(rel string) func(*testing.T, home) {
	return func(t *testing.T, h home) {
		t.Helper()
		if err := os.Remove(filepath.Join(h.dir, rel)); err != nil {
			t.Fatal(err)
		}
	}
}

// A failure after the worktree exists says how to remove it.
func TestSpawnFailuresNameTheWorktreeLeft(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	tm := &tmuxFake{fail: errors.New("tmux died")}
	tm.windows = []string{}
	d := spawnDeps(t, h, (&gitFake{}).run, tm, nil)
	if _, err := Spawn(d, request()); err == nil || !strings.Contains(err.Error(), "cannot list the tmux windows") {
		t.Errorf("tmux down before anything changed: %v", err)
	}
	git := &gitFake{fail: map[string]error{"worktree add": errors.New("disk full")}}
	if _, err := Spawn(spawnDeps(t, h, git.run, &tmuxFake{}, nil), request()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("worktree add failed: %v", err)
	}
	// The trust file is a directory, so Claude's config cannot be written.
	if err := os.MkdirAll(filepath.Join(h.dir, ".claude.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, &tmuxFake{}, nil), request())
	if err == nil || !strings.Contains(err.Error(), "trust ") || !strings.Contains(err.Error(), "worktree remove --force") {
		t.Errorf("trust failed: %v", err)
	}
}
