package hostops

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

// engineCodex is engine's Codex home in h, with the config.toml engine setup
// writes, which it returns.
func (h home) engineCodex(t *testing.T) (dir, config string) {
	t.Helper()
	dir = filepath.Join(h.dir, ".local/share/engine/codex")
	config = "# engine setup writes this block; Codex's own settings follow it.\nsandbox_mode = \"workspace-write\"\n"
	write(t, filepath.Join(dir, "config.toml"), config, 0o600)
	return dir, config
}

// daemon plays the host's Codex as the join reads it: `codex app-server
// daemon version` in engine's Codex home, then ps. It records each call.
type daemon struct {
	version    string
	versionErr error
	ps         string
	psErr      error
	calls      []string
}

func (d *daemon) run(name string, args ...string) (string, error) {
	d.calls = append(d.calls, name+" "+strings.Join(args, " "))
	if name == "ps" {
		return d.ps, d.psErr
	}
	return d.version, d.versionErr
}

// daemonProcess is ps's line for engine's daemon in home with remote
// control on.
func daemonProcess(home string) string {
	return home + "/.local/share/engine/codex/packages/app-server-daemon/releases/0.164.0/bin/codex app-server --remote-control --listen unix:// --managed-daemon"
}

// The states a spawn can meet: engine's daemon connected (running with
// remote control on), down (no daemon) and errored (one whose version reply
// is unreadable).
func connected(home string) *daemon {
	return &daemon{version: `{"status":"running","socketPath":"/x.sock"}`, ps: "/bin/zsh\n" + daemonProcess(home) + "\n"}
}

func down() *daemon {
	return &daemon{versionErr: &session.ExitError{Command: "codex app-server daemon version", Code: 1, Stderr: "Error: failed to connect"}}
}

func errored() *daemon { return &daemon{version: "Error: something odd\n"} }

// codexSpawnDeps are spawnDeps whose host-side Codex answers as d.
func codexSpawnDeps(t *testing.T, h home, tm *tmuxFake, d *daemon, env map[string]string) Deps {
	t.Helper()
	deps := spawnDeps(t, h, (&gitFake{}).run, tm, env)
	deps.Exec, deps.JoinRemote = d.run, true
	return deps
}

func codexRequest(mode string) SpawnRequest {
	r := request()
	r.Harness, r.Mode, r.Model, r.Effort = Codex, mode, "sol", "high"
	return r
}

// tmuxEnv is the window's tmux variables, as tmux sets them.
var tmuxEnv = []string{"TMUX=/tmp/tmux-501/default,42,0", "TMUX_PANE=%9"}

// A hands-off Codex spawn with engine's daemon connected runs in engine's
// Codex home, trusts the worktree in its config.toml, and joins the daemon,
// handing the session's variables to the agent's commands with -c.
func TestSpawnCodexJoins(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	engineHome, before := h.engineCodex(t)
	argv, env := h.fakeCodex(t)
	tm := &tmuxFake{}
	req := codexRequest(HandsOff)
	d := connected(h.dir)
	got, err := Spawn(codexSpawnDeps(t, h, tm, d, map[string]string{"CODEX_HOME": "/operator/codex"}), req)
	if err != nil || got.Warning != "" {
		t.Fatalf("reply %+v, %v", got, err)
	}
	bin := filepath.Join(h.dir, "bin", "codex")
	if want := []string{"env CODEX_HOME=" + engineHome + " " + bin + " app-server daemon version", "ps -ww -eo args="}; !slices.Equal(d.calls, want) {
		t.Errorf("asked\n %q\nwant %q", d.calls, want)
	}
	config, _ := os.ReadFile(filepath.Join(engineHome, "config.toml"))
	if want := before + "\n[projects.\"" + got.Worktree + "\"]\ntrust_level = \"trusted\"\n"; !strings.HasPrefix(string(config), want) {
		t.Errorf("config.toml\n%s\nwant\n%s", config, want)
	}
	if _, err := os.Stat(filepath.Join("/operator/codex", "engine-r-7.config.toml")); !os.IsNotExist(err) {
		t.Errorf("a joined session got a profile: %v", err)
	}
	runWindow(t, tm.command, tmuxEnv...)
	vars := `shell_environment_policy.set={ENGINE_RUN_DIR="` + got.RunDir + `",ENGINE_HARNESS="codex",ENGINE_UNATTENDED="1",` +
		`TMUX="/tmp/tmux-501/default,42,0",TMUX_PANE="%9"}`
	want := []string{"-c", "sandbox_workspace_write.network_access=true", "-m", "sol", "-c", "model_reasoning_effort=high",
		"-C", got.Worktree, "--dangerously-bypass-hook-trust", "-c", vars, "--remote", "unix://", "--", req.Kickoff}
	if args := lines(t, argv); !slices.Equal(args, want) {
		t.Errorf("argv\n got %q\nwant %q", args, want)
	}
	environ := lines(t, env)
	for _, v := range []string{"GH_TOKEN=tok", "ENGINE_RUN_DIR=" + got.RunDir, "ENGINE_HARNESS=codex", "ENGINE_UNATTENDED=1", "CODEX_HOME=" + engineHome} {
		if !slices.Contains(environ, v) {
			t.Errorf("environment lacks %s:\n%s", v, strings.Join(environ, "\n"))
		}
	}
}

// A gated joined session's commands are not marked unattended.
func TestSpawnCodexJoinsGated(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	h.engineCodex(t)
	argv, _ := h.fakeCodex(t)
	tm := &tmuxFake{}
	if _, err := Spawn(codexSpawnDeps(t, h, tm, connected(h.dir), nil), codexRequest(Gated)); err != nil {
		t.Fatal(err)
	}
	runWindow(t, tm.command, tmuxEnv...)
	args := strings.Join(lines(t, argv), " ")
	if !strings.Contains(args, `ENGINE_HARNESS="codex",TMUX=`) || strings.Contains(args, "ENGINE_UNATTENDED") {
		t.Errorf("argv %s", args)
	}
}

// Whatever keeps a session from joining never refuses the spawn: it starts
// embedded, as without a daemon, with its profile in the host's own Codex
// home, and the reply warns, naming why and the command that starts the
// daemon.
func TestSpawnCodexEmbedded(t *testing.T) {
	cases := []struct {
		name, why string
		d         func(home string) *daemon
	}{
		{"down", "engine's Codex daemon is not running", func(string) *daemon { return down() }},
		{"errored", "engine's Codex daemon is unreadable", func(string) *daemon { return errored() }},
		{"stopped", "engine's Codex daemon is stopped", func(string) *daemon { return &daemon{version: `{"status":"stopped"}`} }},
		{"failed to run", "engine's Codex daemon is unreadable (env: boom)", func(string) *daemon { return &daemon{versionErr: errors.New("env: boom")} }},
		{"remote control off", "engine's Codex daemon runs with remote control off", func(home string) *daemon {
			d := connected(home)
			d.ps = strings.ReplaceAll(d.ps, " --remote-control", "")
			return d
		}},
		{"another home's daemon", "remote control off", func(home string) *daemon {
			d := connected(home)
			d.ps = strings.ReplaceAll(d.ps, "/.local/share/engine/codex/", "/.codex/")
			return d
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHome(t, "mac")
			clone := h.clone(t)
			engineHome, before := h.engineCodex(t)
			argv, env := h.fakeCodex(t)
			tm := &tmuxFake{}
			got, err := Spawn(codexSpawnDeps(t, h, tm, c.d(h.dir), nil), codexRequest(HandsOff))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"the Codex session runs embedded, so the ChatGPT and Codex apps do not show it: ", c.why, "`engine setup`"} {
				if !strings.Contains(got.Warning, want) {
					t.Errorf("warning %q lacks %q", got.Warning, want)
				}
			}
			profile, err := os.ReadFile(filepath.Join(h.dir, ".codex", "engine-r-7.config.toml"))
			if err != nil || !strings.Contains(string(profile), `[projects."`+got.Worktree+`"]`) ||
				!strings.Contains(string(profile), `writable_roots = ["`+clone+`"]`) {
				t.Errorf("profile %s, %v", profile, err)
			}
			if config, _ := os.ReadFile(filepath.Join(engineHome, "config.toml")); string(config) != before {
				t.Errorf("engine's config.toml changed:\n%s", config)
			}
			runWindow(t, tm.command, tmuxEnv...)
			args := lines(t, argv)
			if want := []string{"-p", "engine-r-7", "--dangerously-bypass-hook-trust", "--"}; !slices.Equal(args[6:10], want) || slices.Contains(args, "--remote") {
				t.Errorf("argv %q", args)
			}
			if slices.Contains(lines(t, env), "CODEX_HOME="+engineHome) {
				t.Error("an embedded session ran in engine's Codex home")
			}
		})
	}
}

// A launch line that sets what a joined session refuses keeps the session
// embedded, and the daemon is never asked.
func TestSpawnCodexLaunchLineBlocksTheJoin(t *testing.T) {
	for _, flag := range []string{`-c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]'`, "--add-dir /x"} {
		h := newHome(t, "mac")
		h.clone(t)
		h.engineCodex(t)
		h.fakeCodex(t)
		cfg, _ := os.ReadFile(filepath.Join(h.dir, ".config/engine/hosts.toml"))
		write(t, filepath.Join(h.dir, ".config/engine/hosts.toml"),
			strings.Replace(string(cfg), `"{bin} -c sandbox_workspace_write.network_access=true`, `"{bin} `+flag, 1), 0o600)
		d := connected(h.dir)
		got, err := Spawn(codexSpawnDeps(t, h, &tmuxFake{}, d, nil), codexRequest(HandsOff))
		if err != nil || !strings.Contains(got.Warning, "which a joined session refuses") || len(d.calls) != 0 {
			t.Errorf("%s: reply %+v, %v, asked %q", flag, got, err, d.calls)
		}
	}
}

// A host whose processes cannot be listed starts the session embedded.
func TestSpawnCodexCannotListProcesses(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	h.fakeCodex(t)
	d := connected(h.dir)
	d.psErr = errors.New("ps: boom")
	got, err := Spawn(codexSpawnDeps(t, h, &tmuxFake{}, d, nil), codexRequest(HandsOff))
	if err != nil || !strings.Contains(got.Warning, "cannot read engine's Codex daemon's arguments (ps: boom)") {
		t.Errorf("reply %+v, %v", got, err)
	}
}

// A host that may sleep and a daemon that is down are both reported.
func TestSpawnCodexWarnsOfEverything(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	h.fakeCodex(t)
	tm := &tmuxFake{awakeErr: errors.New("no caffeinate")}
	got, err := Spawn(codexSpawnDeps(t, h, tm, down(), nil), codexRequest(HandsOff))
	want := "the host may sleep while the session runs: keeping the host awake: no caffeinate; the Codex session runs embedded"
	if err != nil || !strings.HasPrefix(got.Warning, want) {
		t.Errorf("warning %q, %v; want it to start %q", got.Warning, err, want)
	}
}

// Unless the caller lets sessions join, a Codex spawn never asks the daemon
// its state, adds no --remote and warns of nothing.
func TestSpawnCodexDoesNotJoinByDefault(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	argv, _ := h.fakeCodex(t)
	d := connected(h.dir)
	tm := &tmuxFake{}
	deps := spawnDeps(t, h, (&gitFake{}).run, tm, nil)
	deps.Exec = d.run
	got, err := Spawn(deps, codexRequest(HandsOff))
	if err != nil || got.Warning != "" || len(d.calls) != 0 {
		t.Fatalf("reply %+v, %v, asked %q", got, err, d.calls)
	}
	runWindow(t, tm.command)
	if args := lines(t, argv); slices.Contains(args, "--remote") {
		t.Errorf("argv %q joined remote control", args)
	}
}

// A Claude spawn never asks about Codex's daemon.
func TestSpawnClaudeDoesNotAskTheDaemon(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	d := connected(h.dir)
	deps := spawnDeps(t, h, (&gitFake{}).run, &tmuxFake{}, nil)
	deps.Exec, deps.JoinRemote = d.run, true
	if got, err := Spawn(deps, request()); err != nil || got.Warning != "" || len(d.calls) != 0 {
		t.Errorf("reply %+v, %v, asked %q", got, err, d.calls)
	}
}

// A joined window that fails to open takes its trust with it, and so does
// the release of a joined session.
func TestCodexJoinedTrustIsRemoved(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	engineHome, before := h.engineCodex(t)
	h.fakeCodex(t)
	config := filepath.Join(engineHome, "config.toml")
	_, err := Spawn(codexSpawnDeps(t, h, &tmuxFake{openErr: errors.New("no server")}, connected(h.dir), nil), codexRequest(HandsOff))
	if err == nil || !strings.Contains(err.Error(), "open the tmux window") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(config); string(got) != before {
		t.Errorf("trust left after a failed open:\n%s", got)
	}

	h = newHome(t, "mac")
	h.clone(t)
	engineHome, before = h.engineCodex(t)
	h.fakeCodex(t)
	config = filepath.Join(engineHome, "config.toml")
	tm := &tmuxFake{}
	if _, err := Spawn(codexSpawnDeps(t, h, tm, connected(h.dir), nil), codexRequest(HandsOff)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(config); string(got) == before {
		t.Fatal("the spawn trusted nothing")
	}
	tm.windows = []string{"@9 %9 0 r-7"}
	if r, err := Release(Deps{Run: (&gitFake{}).run, Tmux: tm.tmux(), Home: h.dir}, "r-7", true); err != nil || r.Note != "" {
		t.Errorf("released %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(config); string(got) != before {
		t.Errorf("trust left after the release:\n%s", got)
	}
}

// Only the daemon's own program counts as running with remote control on,
// not a shell or awk that names the path.
func TestRemoteControlOn(t *testing.T) {
	home := "/h/.local/share/engine/codex"
	cases := map[string]bool{
		home + "/packages/app-server-daemon/current/bin/codex app-server --remote-control --listen unix://": true,
		home + "/packages/app-server-daemon/current/bin/codex app-server --listen unix://":                  false,
		"awk -v p=" + home + "/packages/ / --remote-control/":                                               false,
		"/bin/sh -c " + home + "/packages/x --remote-control":                                               false,
		"/h/.codex/packages/app-server-daemon/current/bin/codex app-server --remote-control":                false,
		"": false,
	}
	for ps, want := range cases {
		if got := remoteControlOn("/bin/zsh\n"+ps+"\n", home); got != want {
			t.Errorf("remoteControlOn(%q) = %v, want %v", ps, got, want)
		}
	}
}

// The check joins as a spawn would: engine's config.toml trusts its folder
// while it runs, and its run.json names that folder, which a joined
// session's hooks find their run by.
func TestCheckCodexJoins(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, h, _ := checkDeps(t, f)
	engineHome, before := h.engineCodex(t)
	h.fakeCodex(t)
	d.Exec, d.JoinRemote = connected(h.dir).run, true
	runDir := filepath.Join(h.dir, ".local/share/engine/runs", checkRun)
	work := filepath.Join(runDir, "work")
	var trusted string
	var rec session.Record
	f.act = func() {
		data, _ := os.ReadFile(filepath.Join(engineHome, "config.toml"))
		trusted = string(data)
		rec, _ = session.ReadRecord(runDir)
		write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600)
		writeCheckState(t, d, session.StateEnded)
	}
	r, err := Check(d, Codex, &bytes.Buffer{})
	if err != nil || !r.OK() || !r.Joined {
		t.Fatalf("result %+v, %v", r, err)
	}
	if !strings.Contains(trusted, `[projects."`+work+`"]`) {
		t.Errorf("the session ran without its folder trusted:\n%s", trusted)
	}
	if rec.Worktree != work || rec.Harness != Codex || rec.Mode != HandsOff || rec.Name != CheckName {
		t.Errorf("run.json %+v", rec)
	}
	if got, _ := os.ReadFile(filepath.Join(engineHome, "config.toml")); string(got) != before {
		t.Errorf("trust left:\n%s", got)
	}
	for _, want := range []string{"export CODEX_HOME=" + engineHome, "-C " + work, "--remote unix://", "shell_environment_policy.set="} {
		if !strings.Contains(f.command, want) {
			t.Errorf("command lacks %q:\n%s", want, f.command)
		}
	}
	var report bytes.Buffer
	ReportCheck(&report, r)
	if !strings.Contains(report.String(), "ok   the session joined remote control") {
		t.Errorf("report:\n%s", report.String())
	}
}

// A check that cannot join runs embedded, writes no run.json, and says why.
func TestCheckCodexEmbedded(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, h, _ := checkDeps(t, f)
	h.fakeCodex(t)
	d.Exec, d.JoinRemote = down().run, true
	f.act = func() {
		write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600)
		writeCheckState(t, d, session.StateEnded)
	}
	r, err := Check(d, Codex, &bytes.Buffer{})
	if err != nil || !r.OK() || r.Joined {
		t.Fatalf("result %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(r.RunDir, session.RecordFile)); !os.IsNotExist(err) {
		t.Errorf("run.json: %v", err)
	}
	var report bytes.Buffer
	ReportCheck(&report, r)
	if !strings.Contains(report.String(), "note the Codex session runs embedded, so the ChatGPT and Codex apps do not show it: engine's Codex daemon is not running") {
		t.Errorf("report:\n%s", report.String())
	}
}
