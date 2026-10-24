package run

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

var t0 = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

func ref(n int) tracker.Ref { return tracker.Ref{Repo: "o/r", Number: n} }

// cardBody is a well-formed card of the given tier.
func cardBody(n int, tier string) string {
	return fmt.Sprintf("## Outcome\nx\n## Tests\ny\n## Requires\nnone\n## Harness / tier\n%s\n<!-- engine:card k%d -->\n", tier, n)
}

// behaviour is how a fake executor runs its task, in fake minutes after its
// start: when it claims, opens its PR and merges, and whether it dies or
// waits instead. A zero step never happens.
type behaviour struct {
	claim, review, merge int
	dieAt, waitAt        int
}

var smooth = behaviour{claim: 1, review: 3, merge: 4}

// executor is a session the fake world started.
type executor struct {
	ref     tracker.Ref
	started time.Time
	do      behaviour
	window  session.WindowInfo
	dir     string
	status  string
}

// world is a fake World: an in-memory feature, a host, a clock, and fake
// executors that move their tasks along as the clock runs.
type world struct {
	t        *testing.T
	home     string
	now      time.Time
	feature  tracker.Feature
	slots    int
	plans    func(w *world) // the planner's work, when it is started
	refuse   func(tracker.Ref) error
	failNext map[string]error // the next call of a write fails: "comment", "close"
	killed   []string
	history  []string // the labels set on the feature, in order
	do       map[int]behaviour
	execs    map[tracker.Ref]*executor
	nextID   int
	starts   []tracker.Ref
	comments map[tracker.Ref][]string
	closed   bool
	log      []string
}

func newWorld(t *testing.T, labels []string, cards ...tracker.Card) *world {
	return &world{t: t, home: t.TempDir(), now: t0, slots: 3, do: map[int]behaviour{},
		feature:  tracker.Feature{Ref: ref(1), Title: "F", Open: true, Labels: labels, Tasks: cards},
		execs:    map[tracker.Ref]*executor{},
		comments: map[tracker.Ref][]string{}}
}

func card(n int, tier string, blockedBy ...int) tracker.Card {
	c := tracker.Card{Ref: ref(n), Title: fmt.Sprintf("task %d", n), Open: true, Body: cardBody(n, tier)}
	for _, b := range blockedBy {
		c.BlockedBy = append(c.BlockedBy, tracker.Blocker{Ref: ref(b), Open: true})
	}
	return c
}

func (w *world) card(r tracker.Ref) *tracker.Card {
	for i := range w.feature.Tasks {
		if w.feature.Tasks[i].Ref == r {
			return &w.feature.Tasks[i]
		}
	}
	if r == w.feature.Ref {
		return &tracker.Card{Ref: r, Labels: w.feature.Labels}
	}
	w.t.Fatalf("no card %s", r)
	return nil
}

func (w *world) Feature(tracker.Ref) (tracker.Feature, error) {
	f := w.feature
	f.Labels = slices.Clone(f.Labels)
	f.Tasks = slices.Clone(f.Tasks)
	for i := range f.Tasks {
		f.Tasks[i].Labels = slices.Clone(f.Tasks[i].Labels)
		f.Tasks[i].BlockedBy = slices.Clone(f.Tasks[i].BlockedBy)
	}
	return f, nil
}

func (w *world) Task(r tracker.Ref) (tracker.Card, tracker.Ref, error) {
	return *w.card(r), w.feature.Ref, nil
}

func (w *world) Rows() ([]ps.Row, error) {
	var rows []ps.Row
	for _, c := range w.feature.Tasks {
		e, ok := w.execs[c.Ref]
		if !ok || !c.Open || !hasAny(c.Labels, claimStates) {
			continue
		}
		state := ps.Running
		if e.window.Dead {
			state = ps.Dead
		}
		rows = append(rows, ps.Row{Task: c.Ref.String(), Session: e.window.Name, State: state})
	}
	return rows, nil
}

func (w *world) Windows() ([]session.WindowInfo, error) {
	var ws []session.WindowInfo
	for _, e := range w.execs {
		if e.window.ID != "" {
			ws = append(ws, e.window)
		}
	}
	return ws, nil
}

func (w *world) Agents() ([]session.Agent, error) {
	var as []session.Agent
	for _, e := range w.execs {
		if e.window.ID != "" && !e.window.Dead {
			as = append(as, session.Agent{Name: e.window.Name, Status: e.status})
		}
	}
	return as, nil
}

func (w *world) LatestRun(name string) (session.Run, bool, error) {
	return session.LatestRun(w.home, name)
}

func (w *world) Slots() (int, error) { return w.slots, nil }

func (w *world) NoteDir(r tracker.Ref) (string, error) {
	return filepath.Join(w.home, "notes", fmt.Sprint(r.Number)), mkdir(filepath.Join(w.home, "notes", fmt.Sprint(r.Number)))
}

func (w *world) launch(r tracker.Ref, do behaviour) error {
	name := fmt.Sprintf("r-%d", r.Number)
	w.nextID++
	e := &executor{ref: r, started: w.now, do: do, status: session.AgentBusy,
		window: session.WindowInfo{ID: fmt.Sprintf("@%d", w.nextID), Name: name}}
	dir, err := session.RunDir(w.home, session.RunID(session.Repo{Owner: "o", Name: "r"}, r.Number, w.now))
	if err != nil {
		return err
	}
	e.dir = dir
	w.execs[r] = e
	return session.WriteRecord(dir, session.Record{RunID: filepath.Base(dir), Task: r.String(), Name: name,
		Mode: "hands-off", Tmux: "engine:" + e.window.ID, Spawned: w.now})
}

// Start refuses as engine start does: a window of that name, live or dead,
// or no free slot.
func (w *world) Start(r tracker.Ref) error {
	live := 0
	for _, e := range w.execs {
		if e.window.ID != "" && e.window.Name == fmt.Sprintf("r-%d", r.Number) {
			return fmt.Errorf("the tmux session engine already has a window r-%d", r.Number)
		}
		if e.window.ID != "" && !e.window.Dead {
			live++
		}
	}
	if live >= w.slots {
		return fmt.Errorf("no free slot")
	}
	if w.refuse != nil {
		if err := w.refuse(r); err != nil {
			return err
		}
	}
	w.starts = append(w.starts, r)
	return w.launch(r, w.do[r.Number])
}

func (w *world) StartPlanner(r tracker.Ref) error {
	if w.refuse != nil {
		if err := w.refuse(r); err != nil {
			return err
		}
	}
	w.starts = append(w.starts, r)
	return w.launch(r, behaviour{})
}

func (w *world) AddLabels(r tracker.Ref, labels ...string) error {
	if r == w.feature.Ref {
		w.history = append(w.history, labels...)
	}
	c := w.labels(r)
	for _, l := range labels {
		if !slices.Contains(*c, l) {
			*c = append(*c, l)
		}
	}
	return nil
}

func (w *world) RemoveLabel(r tracker.Ref, label string) error {
	c := w.labels(r)
	*c = slices.DeleteFunc(*c, func(l string) bool { return l == label })
	return nil
}

func (w *world) labels(r tracker.Ref) *[]string {
	if r == w.feature.Ref {
		return &w.feature.Labels
	}
	return &w.card(r).Labels
}

func (w *world) fails(call string) error {
	err := w.failNext[call]
	delete(w.failNext, call)
	return err
}

func (w *world) Comment(r tracker.Ref, body string) error {
	if err := w.fails("comment"); err != nil {
		return err
	}
	w.comments[r] = append(w.comments[r], body)
	return nil
}

func (w *world) Close(tracker.Ref) error {
	if err := w.fails("close"); err != nil {
		return err
	}
	w.closed, w.feature.Open = true, false
	return nil
}

// Operator is "op" on the feature and the planner's machine user on a task,
// which the planner wrote.
func (w *world) Operator(r tracker.Ref) (string, error) {
	if r == w.feature.Ref {
		return "op", nil
	}
	return "bot", nil
}

func (w *world) PullRequests(r tracker.Ref) ([]string, error) {
	return []string{fmt.Sprintf("https://example.test/o/r/pull/%d", 100+r.Number)}, nil
}

func (w *world) Capture(string, int) (string, error) { return "Do you want to proceed?", nil }

func (w *world) Kill(id string) error {
	w.killed = append(w.killed, id)
	for _, e := range w.execs {
		if e.window.ID == id {
			e.window = session.WindowInfo{}
		}
	}
	return nil
}

func (w *world) Now() time.Time { return w.now }

func (w *world) Logf(format string, args ...any) { w.log = append(w.log, fmt.Sprintf(format, args...)) }

// sleep runs the clock forward a minute at a time, and each executor's
// script with it.
func (w *world) sleep(d time.Duration) {
	for end := w.now.Add(d); w.now.Before(end); {
		w.now = w.now.Add(time.Minute)
		for _, e := range w.execs {
			w.step(e)
		}
	}
	if w.now.Sub(t0) > 24*time.Hour {
		w.t.Fatalf("the run never ended:\n%s", strings.Join(w.log, "\n"))
	}
}

// step moves one executor along its script.
func (w *world) step(e *executor) {
	if e.window.ID == "" || e.window.Dead {
		return
	}
	if e.ref == w.feature.Ref {
		if w.plans != nil {
			w.plans(w)
			w.plans = nil
		}
		e.window = session.WindowInfo{}
		return
	}
	at := int(w.now.Sub(e.started) / time.Minute)
	c := w.card(e.ref)
	if !c.Open {
		return
	}
	switch {
	case e.do.dieAt != 0 && at >= e.do.dieAt:
		e.window.Dead = true
	case e.do.waitAt != 0 && at >= e.do.waitAt:
		e.status = session.AgentWaiting
	case e.do.merge != 0 && at >= e.do.merge && c.Open:
		w.merge(c)
		e.window = session.WindowInfo{}
	case e.do.review != 0 && at >= e.do.review && !c.Has(InReview):
		_ = w.RemoveLabel(c.Ref, Running)
		_ = w.AddLabels(c.Ref, InReview)
	case e.do.claim != 0 && at >= e.do.claim && !hasAny(c.Labels, claimStates):
		_ = w.AddLabels(c.Ref, Running)
	}
}

// merge closes a task and unblocks its dependants, as a merged PR does.
func (w *world) merge(c *tracker.Card) {
	c.Open = false
	c.Labels = slices.DeleteFunc(c.Labels, func(l string) bool { return l == InReview || l == Running })
	for i := range w.feature.Tasks {
		for j := range w.feature.Tasks[i].BlockedBy {
			if w.feature.Tasks[i].BlockedBy[j].Ref == c.Ref {
				w.feature.Tasks[i].BlockedBy[j].Open = false
			}
		}
	}
}

func mkdir(dir string) error { return os.MkdirAll(dir, 0o755) }
