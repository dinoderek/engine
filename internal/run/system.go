package run

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tracker"
)

// System is the World of a running engine on this host.
type System struct {
	Gh    tracker.Runner
	Token string
	Home  string
	Host  hosts.Host
	Tmux  *session.Tmux
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
func (s System) Task(ref tracker.Ref) (tracker.Card, error) {
	t, err := tracker.ReadTask(s.Gh, s.Token, ref)
	if err != nil {
		return tracker.Card{}, err
	}
	c := tracker.Card{Ref: ref, Title: t.Title, Open: t.Open, Labels: t.Labels}
	for _, b := range t.OpenBlockers {
		c.BlockedBy = append(c.BlockedBy, tracker.Blocker{Ref: b, Open: true})
	}
	return c, nil
}

func (s System) Rows() ([]ps.Row, error)                  { return s.ListRows() }
func (s System) Windows() ([]session.WindowInfo, error)   { return s.Tmux.Windows() }
func (s System) Agents() ([]session.Agent, error)         { return s.ListAgents() }
func (s System) Slots() (int, error)                      { return s.Host.Slots(), nil }
func (s System) Capture(id string, n int) (string, error) { return s.Tmux.Capture(id, n) }
func (s System) Kill(id string) error                     { return s.Tmux.Kill(id) }
func (s System) Now() time.Time                           { return s.Clock() }

func (s System) LatestRun(name string) (session.Run, bool, error) {
	return session.LatestRun(s.Home, name)
}

// NoteDir is <runs>/<repo>-<n>-coordinator, beside the runs it watches.
func (s System) NoteDir(ref tracker.Ref) (string, error) {
	repo, err := session.ParseRepo(ref.Repo)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.Home, filepath.FromSlash(session.RunsDir), session.TaskName(repo, ref.Number)+"-coordinator")
	return dir, os.MkdirAll(dir, 0o755)
}

func (s System) Start(task tracker.Ref) error {
	return start.Run(start.Options{Ref: task, Mode: start.HandsOff}, s.Starts, s.Out)
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
