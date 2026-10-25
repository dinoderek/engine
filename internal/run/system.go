package run

import (
	"errors"
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
	// Claims is engine ps's source of claims. Rows reads every host's
	// sessions from a Fleet instead of its own calls.
	Claims ps.Source
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

func (s System) Now() time.Time { return s.Clock() }

// Rows settles every claim as engine ps does, from the hosts' replies in f:
// this host's windows and Claude Code sessions too.
func (s System) Rows(f Fleet) ([]ps.Row, error) {
	src := s.Claims
	src.Windows = func() ([]session.WindowInfo, error) {
		st, err := f.status(src.Local)
		var ws []session.WindowInfo
		for _, w := range st.Windows {
			ws = append(ws, session.WindowInfo{ID: w.ID, Name: w.Name, Dead: !w.Alive})
		}
		return ws, err
	}
	src.Agents = func() ([]session.Agent, error) {
		st, err := f.status(src.Local)
		if err == nil && st.AgentsError != "" {
			err = errors.New(st.AgentsError)
		}
		return st.Agents, err
	}
	src.Remote = f.status
	l, err := ps.List(src)
	return l.Rows, err
}

// Release releases a session on the named host.
func (s System) Release(host, name string) (hostops.Released, error) {
	h, ok := s.Config.Hosts[host]
	if !ok {
		return hostops.Released{}, fmt.Errorf("host %q is not in hosts.toml", host)
	}
	return s.Client(h).Release(name, false)
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
	f := Fleet{Local: s.Host.Name, DefaultHarness: s.Config.DefaultHarness, Caps: map[string]int{}, Heavy: s.Config.HeavyCapabilities}
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
