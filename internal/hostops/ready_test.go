package hostops

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readyHost is a host whose hosts.toml puts ~/shims first on PATH, where the
// only gh is a fake that logs each call and answers as ghAnswer says; and a
// bare remote o/r whose dev is devScript.
type readyHost struct {
	home
	remote string // the bare repository's URL
	work   string // a clone that pushes to it
	log    string // what gh and dev logged, in order
	tm     *tmuxFake
}

// devWithDoctor is a dev whose usage lists doctor; its doctor logs where it
// ran and its PATH, and exits as $DOCTOR_EXIT says (0 by default).
const devWithDoctor = `#!/bin/sh
if [ "$1" = doctor ]; then
  echo "doctor in $PWD with PATH=$PATH" >> "$READY_LOG"
  echo "go: missing"
  exit $(cat "$READY_LOG.doctor-exit" 2>/dev/null || echo 0)
fi
echo "usage: ./dev <verb>"
echo "  doctor   check this machine"
`

// devWithoutDoctor says, as a usage should, that it has no doctor verb.
const devWithoutDoctor = "#!/bin/sh\necho 'usage: ./dev run'\necho 'There is no doctor verb: nothing to check.'\n"

func newReadyHost(t *testing.T, dev string) *readyHost {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := &readyHost{home: newHome(t, "mac"), work: t.TempDir()}
	h.log = filepath.Join(h.dir, "ready.log")
	t.Setenv("READY_LOG", h.log)
	cfg := filepath.Join(h.dir, ".config", "engine", "hosts.toml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, cfg, strings.Replace(string(data), `capabilities = ["macos"]`, `capabilities = ["macos"]`+"\npath = [\"~/shims\"]", 1), 0o600)
	write(t, filepath.Join(h.dir, "shims", "gh"), `#!/bin/sh
echo "gh $* in $PWD" >> "$READY_LOG"
if [ -f "$READY_LOG.gh-refused" ]; then echo "X Failed to log in to github.com account octo (keyring)"; exit 1; fi
echo "Logged in to github.com account octo"
`, 0o755)
	base := bareRemote(t)
	h.remote = "file://" + filepath.Join(base, "o", "r.git")
	mustGit(t, h.work, "clone", "--quiet", h.remote, ".")
	h.push(t, dev)
	h.tm = &tmuxFake{}
	return h
}

// push commits dev as the repo's dev and pushes it to main.
func (h *readyHost) push(t *testing.T, dev string) {
	t.Helper()
	write(t, filepath.Join(h.work, "dev"), dev, 0o755)
	mustGit(t, h.work, "add", "dev")
	mustGit(t, h.work, "commit", "--quiet", "--allow-empty", "-m", "dev")
	mustGit(t, h.work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

// deps are the host's ops with real git, and a tmux whose new window runs
// its command at once, through sh, then is gone.
func (h *readyHost) deps(t *testing.T, now time.Time) Deps {
	t.Helper()
	tm := h.tm.tmux()
	inner := tm.Run
	tm.Run = func(name string, args ...string) (string, error) {
		if args[0] == "respawn-pane" {
			out, err := exec.Command("/bin/sh", "-c", args[len(args)-1]).CombinedOutput()
			if err != nil {
				t.Errorf("the probe's window failed: %v\n%s", err, out)
			}
		}
		return inner(name, args...)
	}
	return Deps{Home: h.dir, Run: realGit, Tmux: tm, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
}

func (h *readyHost) logged(t *testing.T) []string {
	t.Helper()
	data, _ := os.ReadFile(h.log)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (h *readyHost) scratch() string {
	return filepath.Join(h.dir, ".local/share/engine/readiness/o/r/worktree")
}

func TestReadyRunsGhThenDoctorInTheScratchWorktreeWithThePathPrefix(t *testing.T) {
	h := newReadyHost(t, devWithDoctor)
	r, err := Ready(h.deps(t, fixedNow()), ReadyRequest{Repo: "o/r", Remote: h.remote})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || len(r.Checks) != 2 || r.Checks[0].Name != GHCheck || r.Checks[1].Name != DoctorCheck || r.Cached {
		t.Fatalf("readiness %+v", r)
	}
	if r.ProbedOn != "mac" || r.Repo != "o/r" || len(r.Head) != 40 || r.Config == "" || !r.At.Equal(fixedNow()) {
		t.Errorf("key %+v", r)
	}
	lines := h.logged(t)
	scratch := h.scratch()
	if len(lines) != 2 || lines[0] != "gh auth status -h github.com in "+scratch ||
		!strings.HasPrefix(lines[1], "doctor in "+scratch+" with PATH="+filepath.Join(h.dir, "shims")+":") {
		t.Errorf("the steps ran as\n%s\nwant gh then doctor in %s, the shims first on PATH", strings.Join(lines, "\n"), scratch)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("the scratch worktree is left: %v", err)
	}
	if !strings.Contains(h.tm.command, "export PATH="+filepath.Join(h.dir, "shims")+`:"$PATH" && `) {
		t.Errorf("window command %s", h.tm.command)
	}
}

func TestReadyReusesItsResultUntilTheHeadChanges(t *testing.T) {
	h := newReadyHost(t, devWithDoctor)
	d := h.deps(t, fixedNow())
	first, err := Ready(d, ReadyRequest{Repo: "o/r", Remote: h.remote})
	if err != nil {
		t.Fatal(err)
	}
	// Once cloned, the clone's own origin serves.
	again, err := Ready(h.deps(t, fixedNow().Add(time.Hour)), ReadyRequest{Repo: "o/r"})
	if err != nil || !again.Cached || again.Head != first.Head || len(h.logged(t)) != 2 {
		t.Fatalf("again %+v, %v; log %v", again, err, h.logged(t))
	}
	h.push(t, devWithDoctor+"# changed\n")
	moved, err := Ready(d, ReadyRequest{Repo: "o/r"})
	if err != nil || moved.Cached || moved.Head == first.Head || len(h.logged(t)) != 4 {
		t.Errorf("after a new head %+v, %v; log %v", moved, err, h.logged(t))
	}
	fresh, err := Ready(d, ReadyRequest{Repo: "o/r", Fresh: true})
	if err != nil || fresh.Cached || len(h.logged(t)) != 6 {
		t.Errorf("fresh %+v, %v", fresh, err)
	}
}

func TestReadyWithoutDoctorRunsOnlyGh(t *testing.T) {
	h := newReadyHost(t, devWithoutDoctor)
	r, err := Ready(h.deps(t, fixedNow()), ReadyRequest{Repo: "o/r", Remote: h.remote})
	if err != nil || !r.OK() || len(r.Checks) != 1 || r.Checks[0].Name != GHCheck {
		t.Fatalf("readiness %+v, %v", r, err)
	}
	if lines := h.logged(t); len(lines) != 1 || !strings.HasPrefix(lines[0], "gh auth status") {
		t.Errorf("log %v", lines)
	}
	var out bytes.Buffer
	ReportReady(&out, r)
	if !strings.Contains(out.String(), "ok   o/r: gh auth status at ") || !strings.Contains(out.String(), "offers no doctor") {
		t.Errorf("report:\n%s", out.String())
	}
}

func TestReadyFailsOnARefusedGhSignInWithItsOutput(t *testing.T) {
	h := newReadyHost(t, devWithDoctor)
	write(t, h.log+".gh-refused", "", 0o600)
	write(t, h.log+".doctor-exit", "3\n", 0o600)
	r, err := Ready(h.deps(t, fixedNow()), ReadyRequest{Repo: "o/r", Remote: h.remote})
	if err != nil {
		t.Fatal(err)
	}
	failed := r.Failed()
	if r.OK() || len(failed) != 2 || failed[0].Name != GHCheck || !strings.Contains(failed[0].Output, "Failed to log in to github.com account octo (keyring)") ||
		failed[1].Name != DoctorCheck || failed[1].Output != "go: missing" {
		t.Fatalf("failed %+v", failed)
	}
	var out bytes.Buffer
	ReportReady(&out, r)
	if !strings.Contains(out.String(), "FAIL o/r: gh auth status at ") || !strings.Contains(out.String(), "     go: missing") {
		t.Errorf("report:\n%s", out.String())
	}
}

func TestReadyRefusesAnUnclonedRepoWithoutARemote(t *testing.T) {
	h := newHome(t, "mac")
	d := Deps{Home: h.dir, Run: realGit, Tmux: (&tmuxFake{}).tmux(), Now: fixedNow}
	if _, err := Ready(d, ReadyRequest{Repo: "o/r"}); !isRefusal(err) || !strings.Contains(err.Error(), "pass --remote") {
		t.Errorf("got %v", err)
	}
	if _, err := Ready(d, ReadyRequest{Repo: "nope"}); !isRefusal(err) {
		t.Errorf("bad repo: %v", err)
	}
}

func TestReusable(t *testing.T) {
	at := fixedNow()
	cached := Readiness{ProbedOn: "mac", Repo: "o/r", Head: "abc", Config: "c1", At: at}
	key := readyKey{"mac", "o/r", "abc", "c1"}
	cases := []struct {
		name string
		key  readyKey
		now  time.Time
		want bool
	}{
		{"unchanged", key, at.Add(time.Hour), true},
		{"just under six hours", key, at.Add(ReadyTTL - time.Second), true},
		{"six hours on", key, at.Add(ReadyTTL), false},
		{"a new head", readyKey{"mac", "o/r", "def", "c1"}, at, false},
		{"a new config", readyKey{"mac", "o/r", "abc", "c2"}, at, false},
		{"another host", readyKey{"box", "o/r", "abc", "c1"}, at, false},
		{"another repo", readyKey{"mac", "o/x", "abc", "c1"}, at, false},
		{"from the future", key, at.Add(-time.Minute), false},
	}
	for _, c := range cases {
		if got := reusable(cached, c.key, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadyStepsThatNeverFinish(t *testing.T) {
	dir := t.TempDir()
	got := readReady(dir)
	if len(got) != 2 || got[0].OK || !strings.Contains(got[0].Output, "gh auth status did not finish within 5m0s") ||
		!strings.Contains(got[1].Output, "./dev doctor did not finish") {
		t.Errorf("got %+v", got)
	}
	if lastLines("a\nb\nc", 2) != "b\nc" || lastLines("a", 2) != "a" {
		t.Error("lastLines")
	}
}

// engine host ready parses its flags into the probe and replies with the
// result's fields inside the envelope.
func TestReadyOpOnTheCommandLine(t *testing.T) {
	h := newReadyHost(t, devWithoutDoctor)
	code, reply, _ := call(t, h.deps(t, fixedNow()), "", "ready", "--json", "--repo", "o/r", "--remote", h.remote, "--fresh")
	if code != 0 || reply["repo"] != "o/r" || reply["probed_on"] != "mac" || reply["host"] != "mac" || reply["protocol"] != float64(Protocol) {
		t.Fatalf("exit %d, reply %v", code, reply)
	}
	if checks, _ := reply["checks"].([]any); len(checks) != 1 {
		t.Errorf("checks %v", reply["checks"])
	}
	if code, reply, _ := call(t, h.deps(t, fixedNow()), "", "ready", "--json", "--repo", "nope"); code != 1 || reply["refused"] != true {
		t.Errorf("a bad repo: exit %d, %v", code, reply)
	}
	if !strings.Contains(Usage, "ready --repo <owner/repo> [--remote <url>] [--fresh]") {
		t.Error("the usage lacks ready")
	}
}

// The usage offers doctor only where a line lists it as a verb.
func TestOffersDoctorMatchesOnlyAListedVerb(t *testing.T) {
	for line, want := range map[string]bool{
		"  doctor   check this machine": true,
		"./dev doctor":                  true,
		"doctor":                        true,
		"There is no doctor verb: none": false,
		"  doctors  plural":             false,
		"  run      see doctor first":   false,
	} {
		err := exec.Command("/bin/sh", "-c", "printf '%s\\n' \"$1\" | grep -qE "+offersDoctor, "sh", line).Run()
		if got := err == nil; got != want {
			t.Errorf("%q: offered %v, want %v", line, got, want)
		}
	}
}
