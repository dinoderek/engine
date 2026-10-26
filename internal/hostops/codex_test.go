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
	got, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, tm, map[string]string{"CODEX_HOME": codexHome}), req)
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
		"-p", "engine-r-7", "--dangerously-bypass-hook-trust", "--", req.Kickoff}
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
	if _, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, tm, nil), codexRequest(Gated)); err != nil {
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
	_, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, tm, nil), codexRequest(HandsOff))
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

func TestCheckCodex(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, h, _ := checkDeps(t, f)
	h.fakeCodex(t)
	profile := filepath.Join(h.dir, ".codex", "engine-"+CheckName+".config.toml")
	var trusted []byte
	f.act = func() {
		trusted, _ = os.ReadFile(profile)
		write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600)
	}
	r, err := Check(d, Codex, &bytes.Buffer{})
	if err != nil || !r.OK() {
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
