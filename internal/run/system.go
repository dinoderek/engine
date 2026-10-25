package run

import (
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tracker"
)

// System is the World of a running engine on this host.
type System struct {
	Gh    tracker.Runner
	Token string
	Home  string
	// Config is hosts.toml, and Host this host in it.
	Config *hosts.Config
	Host   hosts.Host
	// Client reaches a host's ops: in process for this host, else over SSH.
	Client func(hosts.Host) remote.Client
	Tmux   *session.Tmux
	// Agents lists the live Claude Code sessions on this host.
	ListAgents func() ([]session.Agent, error)
	// ListRows is engine ps's listing.
	ListRows func() ([]ps.Row, error)
	// Starts is what engine start launches with.
	Starts start.Deps
	Out    io.Writer
	Clock  func() time.Time
}

var _ World = System{}

func (s System) Feature(ref tracker.Ref) (tracker.Feature, error) {
	return tracker.ReadFeature(s.Gh, s.Token, ref)
}

// Task reads one task as a Card. Only its open blockers are known.
func (s System) Task(ref tracker.Ref) (tracker.Card, tracker.Ref, error) {
	t, err := tracker.ReadTask(s.Gh, s.Token, ref)
	if err != nil {
		return tracker.Card{}, tracker.Ref{}, err
	}
	c := tracker.Card{Ref: ref, Title: t.Title, Open: t.Open, Labels: t.Labels, Body: t.Body, Requires: t.Requires}
	for _, b := range t.OpenBlockers {
		c.BlockedBy = append(c.BlockedBy, tracker.Blocker{Ref: b, Open: true})
	}
	op := ref
	if t.Parent != nil {
		op = *t.Parent
	}
	return c, op, nil
}

func (s System) Rows() ([]ps.Row, error)                  { return s.ListRows() }
func (s System) Windows() ([]session.WindowInfo, error)   { return s.Tmux.Windows() }
func (s System) Agents() ([]session.Agent, error)         { return s.ListAgents() }
func (s System) Capture(id string, n int) (string, error) { return s.Tmux.Capture(id, n) }
func (s System) Kill(id string) error                     { return s.Tmux.Kill(id) }
func (s System) Now() time.Time                           { return s.Clock() }

func (s System) LatestRun(name string) (session.Run, bool, error) {
	return session.LatestRun(s.Home, name)
}

// NoteDir is <runs>/<repo>-<n>-coordinator, beside the runs it watches. It
// is made by the first note written there, not by asking for the path.
func (s System) NoteDir(ref tracker.Ref) (string, error) {
	repo, err := session.ParseRepo(ref.Repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Home, filepath.FromSlash(session.RunsDir), session.TaskName(repo, ref.Number)+"-coordinator"), nil
}

func (s System) Start(task tracker.Ref, host string) error {
	return start.Run(start.Options{Ref: task, Mode: start.HandsOff, Host: host}, s.Starts, s.Out)
}

// Fleet reads every host's status, one call per host, all at once.
func (s System) Fleet() Fleet {
	f := Fleet{Local: s.Host.Name, DefaultHarness: s.Config.DefaultHarness, Caps: map[string]int{}}
	for name, h := range s.Config.Harnesses {
		f.Caps[name] = h.MaxSessions
	}
	names := s.Config.HostNames()
	f.Hosts = make([]HostLoad, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			h := s.Config.Hosts[name]
			st, err := s.Client(h).Status()
			f.Hosts[i] = load(h, st, err)
		})
	}
	wg.Wait()
	return f
}

// load is a host's load from its status: its free slots, and its live
// sessions by the harness their newest run names. A window with no run,
// such as engine run's own, holds a slot but no harness's session.
func load(h hosts.Host, st hostops.Status, err error) HostLoad {
	l := HostLoad{Name: h.Name, Harnesses: h.Harnesses, Capabilities: h.Capabilities, Sessions: map[string]int{}}
	if err != nil {
		l.Unreachable = err.Error()
		return l
	}
	l.FreeSlots = st.FreeSlots
	harness := map[string]string{}
	for _, r := range st.Runs {
		harness[r.Name] = r.Record.Harness
	}
	for _, w := range st.Windows {
		if hn := harness[w.Name]; w.Alive && hn != "" {
			l.Sessions[hn]++
		}
	}
	return l
}

func (s System) StartPlanner(feature tracker.Ref) error {
	return start.RunPlanner(feature, s.Starts, s.Out)
}

func (s System) AddLabels(ref tracker.Ref, labels ...string) error {
	return tracker.AddLabels(s.Gh, s.Token, ref, labels...)
}

func (s System) RemoveLabel(ref tracker.Ref, label string) error {
	return tracker.RemoveLabel(s.Gh, s.Token, ref, label)
}

func (s System) Comment(ref tracker.Ref, body string) error {
	_, err := tracker.Comment(s.Gh, s.Token, ref, body)
	return err
}

func (s System) Close(ref tracker.Ref) error { return tracker.CloseCompleted(s.Gh, s.Token, ref) }

func (s System) Operator(ref tracker.Ref) (string, error) {
	return tracker.Operator(s.Gh, s.Token, ref)
}

func (s System) PullRequests(ref tracker.Ref) ([]string, error) {
	return tracker.PullRequests(s.Gh, s.Token, ref)
}

// Logf prints one timestamped line.
func (s System) Logf(format string, args ...any) {
	fmt.Fprintf(s.Out, "%s %s\n", s.Clock().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
