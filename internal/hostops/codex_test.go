package hostops

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// fakeCodex installs ~/bin/codex, the host's pinned Codex, as a script that
// records its arguments, one per line, and its environment, and returns the
// file each goes to.
func (h home) fakeCodex(t *testing.T) (argv, env string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "codex")
	write(t, filepath.Join(h.dir, "bin", "codex"),
		"#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > "+hosts.ShellWord(out+".argv")+
			"\nenv > "+hosts.ShellWord(out+".env")+"\n", 0o755)
	return out + ".argv", out + ".env"
}

// runWindow runs a window's command as tmux would, with outer as the tmux
// server's environment, and waits for it.
func runWindow(t *testing.T, command string, outer ...string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, outer...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the window's command: %v\n%s", err, out)
	}
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// daemon plays `codex remote-control --json` with the reply it is given, and
// records the commands it was asked to run.
type daemon struct {
	out   string
	err   error
	calls []string
}

func (d *daemon) run(name string, args ...string) (string, error) {
	d.calls = append(d.calls, name+" "+strings.Join(args, " "))
	return d.out, d.err
}

// up, down and errored are the daemon states a spawn can meet.
func up() *daemon   { return &daemon{out: `{"status":"connected","serverName":"mac"}`} }
func down() *daemon { return &daemon{out: `{"status":"disabled"}`} }
func errored() *daemon {
	return &daemon{err: &session.ExitError{Command: "codex remote-control --json", Code: 1,
		Stderr: "ERROR codex_app_server: ignoring a key\n  user: `features.rcmp_client` is ignored.\nError: Remote control is enabled on mac but the connection is errored.\n"}}
}

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

// A hands-off Codex spawn trusts the worktree in engine's profile, runs the
// host's launch line, then the profile, the hook-trust bypass and the
// kickoff after --, with the session's variables exported.
func TestSpawnCodex(t *testing.T) {
	h := newHome(t, "mac")
	clone := h.clone(t)
	argv, env := h.fakeCodex(t)
	codexHome := filepath.Join(t.TempDir(), "codex home")
	tm := &tmuxFake{}
	req := codexRequest(HandsOff)
	d := up()
	got, err := Spawn(codexSpawnDeps(t, h, tm, d, map[string]string{"CODEX_HOME": codexHome}), req)
	if err != nil {
		t.Fatal(err)
	}
	worktree := clone + "-worktrees/task-r-7"
	if run, ok, err := session.LatestRun(h.dir, "r-7"); !ok || err != nil || run.Record.Harness != Codex {
		t.Errorf("run.json %+v, %v", run.Record, err)
	}
	profile, err := os.ReadFile(filepath.Join(codexHome, "engine-r-7.config.toml"))
	if err != nil || !strings.Contains(string(profile), "[projects.\""+worktree+"\"]\ntrust_level = \"trusted\"\n") {
		t.Errorf("profile %s, %v", profile, err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("Claude's config was written: %v", err)
	}
	runWindow(t, tm.command)
	want := []string{"-c", `sandbox_workspace_write.writable_roots=["` + clone + `"]`, "-m", "sol", "-c", "model_reasoning_effort=high",
		"-p", "engine-r-7", "--dangerously-bypass-hook-trust", "--remote", "unix://", "--", req.Kickoff}
	if wantCalls := []string{filepath.Join(h.dir, "bin", "codex") + " remote-control --json"}; !slices.Equal(d.calls, wantCalls) {
		t.Errorf("asked the daemon %q, want %q", d.calls, wantCalls)
	}
	if got.Warning != "" {
		t.Errorf("warning %q with the daemon up", got.Warning)
	}
	if args := lines(t, argv); !slices.Equal(args, want) {
		t.Errorf("argv\n got %q\nwant %q", args, want)
	}
	vars := lines(t, env)
	for _, v := range []string{"GH_TOKEN=tok", "ENGINE_RUN_DIR=" + got.RunDir, "ENGINE_HARNESS=codex", "ENGINE_UNATTENDED=1", "CODEX_HOME=" + codexHome} {
		if !slices.Contains(vars, v) {
			t.Errorf("environment lacks %s:\n%s", v, strings.Join(vars, "\n"))
		}
	}
}

// A gated Codex session is not marked unattended, and without CODEX_HOME the
// profile goes to ~/.codex and the window drops the tmux server's own.
func TestSpawnCodexGatedDefaultHome(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	_, env := h.fakeCodex(t)
	tm := &tmuxFake{}
	if _, err := Spawn(codexSpawnDeps(t, h, tm, up(), nil), codexRequest(Gated)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".codex", "engine-r-7.config.toml")); err != nil {
		t.Errorf("profile: %v", err)
	}
	runWindow(t, tm.command, "ENGINE_UNATTENDED=1", "CODEX_HOME=/elsewhere")
	for _, v := range lines(t, env) {
		if strings.HasPrefix(v, "ENGINE_UNATTENDED=") || strings.HasPrefix(v, "CODEX_HOME=") {
			t.Errorf("the window kept %s", v)
		}
	}
}

// A daemon that is not up never refuses the spawn: the session starts
// without --remote, and the reply warns, naming the command that starts the
// daemon and what the daemon said.
func TestSpawnCodexWithoutRemoteControl(t *testing.T) {
	cases := []struct {
		name, state string
		d           *daemon
	}{
		{"disabled", "is disabled", down()},
		{"errored", "is errored (Remote control is enabled on mac but the connection is errored.)", errored()},
		{"connecting", "is connecting", &daemon{out: `{"status":"connecting"}`}},
		{"unreadable", "is unreadable", &daemon{out: "Remote control is disabled on mac\n"}},
		{"no status", "is unreadable", &daemon{out: "{}"}},
		{"no stderr", "is errored on this host", &daemon{err: &session.ExitError{Command: "codex remote-control --json", Code: 1}}},
		{"failed to run", "is unreadable (codex: boom)", &daemon{err: errors.New("codex: boom")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHome(t, "mac")
			h.clone(t)
			argv, _ := h.fakeCodex(t)
			tm := &tmuxFake{}
			got, err := Spawn(codexSpawnDeps(t, h, tm, c.d, nil), codexRequest(HandsOff))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Codex remote control " + c.state, "`codex remote-control start`"} {
				if !strings.Contains(got.Warning, want) {
					t.Errorf("warning %q lacks %q", got.Warning, want)
				}
			}
			runWindow(t, tm.command)
			if args := lines(t, argv); slices.Contains(args, "--remote") || args[len(args)-2] != "--" {
				t.Errorf("argv %q joined remote control or lost the kickoff", args)
			}
		})
	}
}

// A host that may sleep and a daemon that is down are both reported.
func TestSpawnCodexWarnsOfEverything(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	h.fakeCodex(t)
	tm := &tmuxFake{awakeErr: errors.New("no caffeinate")}
	got, err := Spawn(codexSpawnDeps(t, h, tm, down(), nil), codexRequest(HandsOff))
	want := "the host may sleep while the session runs: keeping the host awake: no caffeinate; Codex remote control is disabled on this host"
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
	d := up()
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
	d := up()
	deps := spawnDeps(t, h, (&gitFake{}).run, &tmuxFake{}, nil)
	deps.Exec, deps.JoinRemote = d.run, true
	if got, err := Spawn(deps, request()); err != nil || got.Warning != "" || len(d.calls) != 0 {
		t.Errorf("reply %+v, %v, asked %q", got, err, d.calls)
	}
}

func TestSpawnCodexRefusals(t *testing.T) {
	codex := codexRequest(HandsOff)
	unknown := request()
	unknown.Harness = "gemini"
	cases := []spawnRefusal{
		{name: "no codex binary", req: codex, want: "is not an executable codex binary"},
		{name: "host does not run codex", host: "cl", req: codex, want: "host cl does not run codex"},
		{name: "unknown harness", req: unknown, want: `unknown harness "gemini": want claude or codex`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkSpawnRefusal(t, c) })
	}
}

// A window that fails to open takes its profile with it.
func TestSpawnCodexRemovesTheProfileWhenTheWindowFails(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	h.fakeCodex(t)
	tm := &tmuxFake{openErr: errors.New("no server")}
	_, err := Spawn(codexSpawnDeps(t, h, tm, up(), nil), codexRequest(HandsOff))
	if err == nil || !strings.Contains(err.Error(), "open the tmux window") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".codex", "engine-r-7.config.toml")); !os.IsNotExist(err) {
		t.Errorf("profile left: %v", err)
	}
}

func TestReleaseDeletesTheCodexProfile(t *testing.T) {
	h := newHome(t, "mac")
	profile := filepath.Join(h.dir, ".codex", "engine-r-7.config.toml")
	write(t, profile, "x\n", 0o600)
	tm := &tmuxFake{windows: []string{"@5 %5 0 r-7"}}
	r, err := Release(Deps{Tmux: tm.tmux(), Home: h.dir}, "r-7", true)
	if err != nil || !r.WindowClosed || r.Note != "" {
		t.Errorf("released %+v, %v", r, err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Errorf("profile left: %v", err)
	}
	// With none to delete, release says nothing of it.
	if r, err := Release(Deps{Tmux: tm.tmux(), Home: h.dir}, "r-8", false); err != nil || r.Note != "" {
		t.Errorf("released %+v, %v", r, err)
	}
}

func TestReleaseNotesAProfileItCannotDelete(t *testing.T) {
	h := newHome(t, "mac")
	// A directory with something in it is a profile os.Remove refuses.
	write(t, filepath.Join(h.dir, ".codex", "engine-r-7.config.toml", "x"), "x\n", 0o600)
	r, err := Release(Deps{Tmux: (&tmuxFake{windows: []string{"@1 %1 0 other"}}).tmux(), Home: h.dir}, "r-7", false)
	if err != nil || !strings.Contains(r.Note, "engine-r-7.config.toml") {
		t.Errorf("released %+v, %v", r, err)
	}
}

// writeCheckState plays the session's hook: it writes the state file in the
// check's run directory.
func writeCheckState(t *testing.T, d Deps, state string) {
	t.Helper()
	runDir := filepath.Join(d.Home, ".local/share/engine/runs", checkRun)
	write(t, filepath.Join(runDir, session.StateFile), `{"state":"`+state+`","event":"Stop","time":"2026-01-02T03:04:05Z"}`+"\n", 0o600)
}

func TestCheckCodex(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, h, _ := checkDeps(t, f)
	h.fakeCodex(t)
	profile := filepath.Join(h.dir, ".codex", "engine-"+CheckName+".config.toml")
	var trusted []byte
	f.act = func() {
		trusted, _ = os.ReadFile(profile)
		write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600)
		writeCheckState(t, d, session.StateEnded)
	}
	r, err := Check(d, Codex, &bytes.Buffer{})
	if err != nil || !r.OK() || !r.Signalled || r.State != session.StateEnded {
		t.Fatalf("result %+v, %v", r, err)
	}
	work := filepath.Join(r.RunDir, "work")
	if !strings.Contains(string(trusted), `[projects."`+work+`"]`) {
		t.Errorf("the session ran without its folder trusted: %s", trusted)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Errorf("profile left: %v", err)
	}
	for _, want := range []string{"export ENGINE_HARNESS=codex", "-m luna", "-p engine-" + CheckName, "--dangerously-bypass-hook-trust",
		hosts.ShellWord(CodexCheckKickoff(filepath.Join(checkTmp, "engine-"+checkRun)))} {
		if !strings.Contains(f.command, strings.ReplaceAll(want, "'", `'\''`)) {
			t.Errorf("command lacks %q:\n%s", want, f.command)
		}
	}
	if _, err := Check(d, "gemini", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Errorf("unknown harness: %v", err)
	}
}

// A request the hook left to auto-review is no denial: auto-review decided
// it without a prompt, so the check passes and lists it apart.
func TestCheckCodexAutoReviewIsNoDenial(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, h, _ := checkDeps(t, f)
	h.fakeCodex(t)
	f.act = func() {
		runDir := filepath.Join(d.Home, ".local/share/engine/runs", checkRun)
		write(t, filepath.Join(runDir, session.PermissionLog),
			`{"event":"PermissionRequest","tool":"Bash","input":{"command":"tmux kill-window"},"decision":"`+hook.AutoReview+`"}`+"\n", 0o600)
		write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600)
		writeCheckState(t, d, session.StateWorking)
	}
	r, err := Check(d, Codex, &bytes.Buffer{})
	if err != nil || !r.OK() || len(r.Denied) != 0 || len(r.Reviewed) != 1 {
		t.Fatalf("result %+v, %v", r, err)
	}
	var report bytes.Buffer
	ReportCheck(&report, r)
	for _, want := range []string{"ok   no permission request was denied", "auto-review decided", `Bash {"command":"tmux kill-window"}: ` + hook.AutoReview} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, report.String())
		}
	}
	if strings.Contains(report.String(), "FAIL") || strings.Contains(report.String(), "would stop") {
		t.Errorf("report:\n%s", report.String())
	}
}

// Codex's launch line puts {clone} inside quotes, where only a plain word is
// safe: every clone path engine makes under a plain home is one.
func TestClonePathIsAPlainWord(t *testing.T) {
	for _, repo := range []string{"o/r", "Brotherhood-of-Ghisa/boga_3.app", "dinoderek/engine"} {
		r, err := session.ParseRepo(repo)
		if err != nil {
			t.Fatal(err)
		}
		path, err := session.Repos{Home: "/Users/me"}.Path(r)
		if err != nil || hosts.ShellWord(path) != path {
			t.Errorf("%s: clone path %s is not a plain word (%v)", repo, path, err)
		}
	}
}

// A Codex session that did every step but whose hooks wrote no state file
// fails the check: engine would never see it work, wait or end.
func TestCheckCodexFailsWithoutAStateFile(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, h, _ := checkDeps(t, f)
	h.fakeCodex(t)
	f.act = func() { write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600) }
	var out bytes.Buffer
	ok, err := RunCheck(d, Codex, &out)
	if ok || err != nil {
		t.Fatalf("ok=%v, %v\n%s", ok, err, out.String())
	}
	for _, want := range []string{"ok   the session closed its own window", "ok   the session did every step",
		"FAIL the session's hooks wrote its state (no state file in the run directory)", "would stop"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

// A Codex session that sits on a prompt stalls the check; the report shows
// its last screen and the state its hooks recorded.
func TestCheckCodexStallsOnAPrompt(t *testing.T) {
	f := &checkTmux{openPolls: 1 << 30, screen: "Allow Codex to run `rm`? [y/n]\n"}
	d, h, _ := checkDeps(t, f)
	h.fakeCodex(t)
	f.act = func() { writeCheckState(t, d, session.StateWaiting) }
	var out bytes.Buffer
	ok, err := RunCheck(d, Codex, &out)
	if ok || err != nil || !f.killed {
		t.Fatalf("ok=%v, %v, killed %v\n%s", ok, err, f.killed, out.String())
	}
	for _, want := range []string{"FAIL the session closed its own window", "Allow Codex to run `rm`?", "ok   the session's hooks wrote its state (last: waiting)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

// Claude's check needs no state file: its state comes from claude agents.
func TestCheckClaudeNeedsNoStateFile(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, _, _ := checkDeps(t, f)
	f.act = func() { write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600) }
	var out bytes.Buffer
	if ok, err := RunCheck(d, Claude, &out); !ok || err != nil || strings.Contains(out.String(), "hooks wrote") {
		t.Errorf("ok=%v, %v\n%s", ok, err, out.String())
	}
}
