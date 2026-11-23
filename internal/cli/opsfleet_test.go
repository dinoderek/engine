package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/controlrun"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// opsSlug is the fleet's one repository.
const opsSlug = "fleet/app"

// opsFleet is the world of the coordinator's acceptance tests: the real
// control server and coordinator, wired as engine serve wires them, on the
// first host's database; hosts reached over a fake ssh that runs each
// host's real `engine host <op>` through Run, with a fake tmux server and
// Claude Code CLI and real git (opshost_test.go, opstmux_test.go); and
// GitHub, whose origin is a bare repository (opsgithub_test.go). Tests act
// as the operator with engine commands, and as each session with the
// engine commands, git and gh it would run.
type opsFleet struct {
	t      *testing.T
	clock  *stackClock
	names  []string
	hosts  map[string]*opsHost
	github *fakeGitHub
	// Each host's extra hosts.toml lines.
	extra map[string]string

	token, url string
	store      *control.Store
	coord      *controlrun.Coordinator
	obs        *controlrun.Observer
	server     *http.Server
	served     chan struct{}
	// opFile holds the operator credential; logs is the coordinator's log.
	opFile string
	logs   *syncBuffer

	repo, plan control.Item
}

// newOpsFleet is a fleet of the named hosts, the first running the control
// server, each with two Claude slots and ~/fakebin first on PATH, and a
// repo with a planned, coordinated plan and no tasks yet.
func newOpsFleet(t *testing.T, names ...string) *opsFleet {
	t.Helper()
	for _, tool := range []string{"git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is absent")
		}
	}
	f := &opsFleet{t: t, names: names, hosts: map[string]*opsHost{}, extra: map[string]string{}, logs: &syncBuffer{},
		clock: &stackClock{at: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}}
	for i, name := range names {
		f.hosts[name] = newOpsHost(f, name, 4100+i)
	}
	f.github = newGitHub(f, opsSlug, f.hosts[names[0]])
	f.writeConfig()
	f.start()
	f.repo = f.create(control.Item{Kind: "repo", Title: "App", Slug: opsSlug, Remote: f.github.origin})
	f.plan = f.create(control.Item{Kind: "plan", Repo: f.repo.ID, Title: "Harden", Body: "## Outcome\n\nHardened.\n"})
	f.call(control.Request{Op: "approve", ID: f.plan.ID, Version: f.plan.Version})
	f.call(control.Request{Op: "state", ID: f.plan.ID, Version: f.get(f.plan.ID).Version, State: "planned"})
	f.call(control.Request{Op: "run", ID: f.plan.ID})
	return f
}

// writeConfig writes the fleet's hosts.toml and each host's host file.
func (f *opsFleet) writeConfig() {
	var b strings.Builder
	b.WriteString("default_harness = \"claude\"\ngb_per_session = 8\nreserve_gb = 8\n\n[harness.claude]\nmax_sessions = 8\n" +
		"launch = \"{bin} --model {model} --effort {effort} -n {name} --remote-control {name}\"\n" +
		"tiers.deep     = { model = \"o\", effort = \"high\" }\ntiers.standard = { model = \"s\", effort = \"high\" }\ntiers.light    = { model = \"h\", effort = \"medium\" }\n")
	for _, name := range f.names {
		fmt.Fprintf(&b, "\n[host.%s]\naddress = \"dev@%s\"\nharnesses = [\"claude\"]\nram_gb = 24\nbin = { claude = \"~/bin/claude\" }\npath = [\"~/fakebin\"]\n%s",
			name, name, f.extra[name])
	}
	for _, h := range f.hosts {
		dir := hosts.ConfigDir(h.home)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			f.t.Fatal(err)
		}
		writeFile(f.t, filepath.Join(dir, "hosts.toml"), b.String())
		writeFile(f.t, filepath.Join(dir, "host"), h.name+"\n")
	}
}

// start serves the control API and wires the coordinator, the session
// observer and the host board to it as engine serve does, every host read
// going over the fake ssh.
func (f *opsFleet) start() {
	f.t.Helper()
	home := f.hosts[f.names[0]].home
	store, err := control.Open(filepath.Join(home, controlcmd.ServerDBPath))
	if err != nil {
		f.t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		f.t.Fatal(err)
	}
	f.store, f.url, f.token = store, "http://"+ln.Addr().String(), control.Secret()
	f.opFile = filepath.Join(f.t.TempDir(), "server-token")
	writeFile(f.t, f.opFile, f.token+"\n")
	client := func(h hosts.Host) remote.Client { return remote.SSH{Name: h.Name, Address: h.Address, Exec: f.ssh} }
	logf := func(format string, args ...any) { fmt.Fprintf(f.logs, format+"\n", args...) }
	service := control.Service{Store: store, Now: f.clock.now}
	c := &controlrun.Coordinator{Service: service, URL: f.url, Home: home, Client: client, Logf: logf}
	c.Ready = &controlrun.Readiness{Client: client, Now: f.clock.now, Logf: logf}
	c.Wake = func(s reconcile.Session, text string) error {
		return f.keys(s, func(tm *session.Tmux, id string) error { return tm.Type(id, text) })
	}
	c.Resume = func(s reconcile.Session, logged func(string) error) error {
		return f.keys(s, func(tm *session.Tmux, id string) error { return tm.Submit(id, controlrun.ResumeText, logged) })
	}
	f.obs = &controlrun.Observer{Store: store, Client: client, Now: f.clock.now, Logf: logf,
		Config: func() (*hosts.Config, error) { cfg, _, err := hosts.Open(hosts.ConfigDir(home)); return cfg, err }}
	board := &controlrun.HostBoard{Now: f.clock.now}
	service.Reconcile, service.Observe, service.Hosts = c.ConfirmEnded, f.obs.Observation, board.Status
	c.Service, c.Observer, c.Board = service, f.obs, board
	f.coord = c
	f.server = &http.Server{Handler: control.Handler(service, f.token, logf), ReadHeaderTimeout: 5 * time.Second}
	f.served = make(chan struct{})
	go func() {
		defer close(f.served)
		if err := f.server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			f.t.Errorf("control server: %v", err)
		}
	}()
	f.t.Cleanup(f.stop)
}

func (f *opsFleet) stop() {
	_ = f.server.Close()
	<-f.served
	f.coord.Ready.Settle()
	f.obs.Settle()
	_ = f.store.Close()
}

// keys types into a session's window on its host, as engine serve's wake
// and resume do, once its window is still the session's and live.
func (f *opsFleet) keys(s reconcile.Session, keys func(*session.Tmux, string) error) error {
	tm := f.hosts[s.Host].tmux.tmux()
	wins, err := tm.Windows()
	if err != nil {
		return err
	}
	for _, w := range wins {
		if w.ID == s.WindowID && w.Name == s.Name && !w.Dead {
			return keys(tm, w.ID)
		}
	}
	return errors.New("execution window changed")
}

// pass runs one coordinator pass and waits for the readiness probes and
// observations it started, so the next pass sees their answers.
func (f *opsFleet) pass() {
	f.t.Helper()
	if err := f.coord.Pass(context.Background()); err != nil {
		f.t.Fatalf("pass: %v\n%s", err, f.logs)
	}
	f.coord.Ready.Settle()
	f.obs.Settle()
}

// passes runs n passes.
func (f *opsFleet) passes(n int) {
	f.t.Helper()
	for range n {
		f.pass()
	}
}

func (f *opsFleet) operator() control.Client { return control.Client{URL: f.url, Token: f.token} }

func (f *opsFleet) call(r control.Request) control.Response {
	f.t.Helper()
	out, err := f.operator().Call(context.Background(), r)
	if err != nil {
		f.t.Fatalf("%s %s: %v", r.Op, r.ID, err)
	}
	return out
}

func (f *opsFleet) create(item control.Item) control.Item {
	f.t.Helper()
	return *f.call(control.Request{Op: "create", Item: item}).Item
}

func (f *opsFleet) get(id string) control.Item {
	f.t.Helper()
	return *f.call(control.Request{Op: "get", ID: id}).Item
}

// task creates a task of the fleet's plan.
func (f *opsFleet) task(title string, depends ...string) control.Item {
	f.t.Helper()
	return f.create(control.Item{Kind: "task", Repo: f.repo.ID, Plan: f.plan.ID, Title: title, Body: "## Outcome\n\n" + title + ".\n\n## Tests\n\n- Unit.\n", Depends: depends})
}

// engine runs an engine command as the operator, on the first host.
func (f *opsFleet) engine(args ...string) (int, string, string) {
	return f.hosts[f.names[0]].engine(map[string]string{"ENGINE_SERVER_URL": f.url, "ENGINE_SERVER_TOKEN_FILE": f.opFile}, "", args...)
}

// updates are the item's updates, oldest first.
func (f *opsFleet) updates(id string) []control.Update {
	f.t.Helper()
	return f.call(control.Request{Op: "updates", ID: id}).Updates
}

// said are the bodies of the item's updates of kind.
func (f *opsFleet) said(id, kind string) []string {
	var out []string
	for _, u := range f.updates(id) {
		if u.Kind == kind {
			out = append(out, u.Body)
		}
	}
	return out
}

// attempts are the item's attempts, oldest first.
func (f *opsFleet) attempts(id string) []control.Attempt {
	f.t.Helper()
	return f.call(control.Request{Op: "attempts", ID: id}).Attempts
}

// opsSession is a task's running session as its host launched it: the run
// directory with its credential, and the worktree on the task's branch.
type opsSession struct {
	f                 *opsFleet
	h                 *opsHost
	task, name, runID string
	dir, worktree     string
	branch            string
}

// session is the run of the task's current attempt, on whichever host
// launched it.
func (f *opsFleet) session(task control.Item) *opsSession {
	f.t.Helper()
	name := opsWindow(task)
	runID := name + "-" + f.get(task.ID).Attempt
	for _, h := range f.hosts {
		dir := filepath.Join(h.home, filepath.FromSlash(session.RunsDir), runID)
		if rec, err := session.ReadRecord(dir); err == nil {
			return &opsSession{f: f, h: h, task: task.ID, name: name, runID: runID, dir: dir, worktree: rec.Worktree, branch: rec.Branch}
		}
	}
	f.t.Fatalf("%s has no run %s on any host\n%s", task.ID, runID, f.logs)
	return nil
}

// opsWindow is the task's window name on every host.
func opsWindow(task control.Item) string { return fmt.Sprintf("app-%d", control.Number(task.ID)) }

// runVars is the session's environment: its run's server endpoint,
// credential and attempt, and its run directory.
func (s *opsSession) runVars() map[string]string {
	s.f.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, "server-session.json"))
	if err != nil {
		s.f.t.Fatal(err)
	}
	var conn struct{ URL, Attempt string }
	if err = json.Unmarshal(data, &conn); err != nil {
		s.f.t.Fatal(err)
	}
	return map[string]string{"ENGINE_SERVER_URL": conn.URL, "ENGINE_SERVER_TOKEN_FILE": filepath.Join(s.dir, "server-token"),
		"ENGINE_ATTEMPT_ID": conn.Attempt, session.RunDirVar: s.dir}
}

// engine runs an engine command as the session, in its environment.
func (s *opsSession) engine(args ...string) (int, string, string) {
	return s.h.engine(s.runVars(), "", args...)
}

// must runs an engine command as the session and fails the test unless it
// exits 0.
func (s *opsSession) must(args ...string) string {
	s.f.t.Helper()
	code, out, errOut := s.engine(args...)
	if code != 0 {
		s.f.t.Fatalf("%s: engine %s: exit %d: %s%s", s.task, strings.Join(args, " "), code, errOut, out)
	}
	return out
}

// commit commits a file in the worktree and pushes the branch, returning
// the new head.
func (s *opsSession) commit(file, message string) string {
	writeFile(s.f.t, filepath.Join(s.worktree, file), message+"\n")
	s.h.git(s.worktree, "add", file)
	s.h.git(s.worktree, "commit", "--quiet", "-m", message)
	s.h.git(s.worktree, "push", "--quiet", "origin", "HEAD:refs/heads/"+s.branch)
	return s.h.git(s.worktree, "rev-parse", "HEAD")
}

// review opens a PR of the branch at head with green checks, sets the task
// in review on it and asks for the merge; it returns the PR's number.
func (s *opsSession) review(head, title, body string) int {
	s.f.t.Helper()
	n := s.f.github.open(s.branch, head, title, body)
	s.f.github.check(n, "test", "SUCCESS", s.f.clock.now())
	s.inReview(n)
	s.must("task", "merge-ready", s.task, "--pr", s.f.github.url(n), "--head", head, "--method", "squash", "--verdict", "APPROVED")
	return n
}

// inReview sets the task in review with PR n.
func (s *opsSession) inReview(n int) {
	s.f.t.Helper()
	item := s.f.get(s.task)
	s.must("task", "state", s.task, "--version", fmt.Sprint(item.Version), "--state", "in-review", "--pr", s.f.github.url(n))
}
