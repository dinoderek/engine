package run

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The coordinator's own notes: that it has reported the plan's lint
// failures, and when a task's start last failed.
const (
	LintNoted   = "lint-noted"
	StartFailed = "start-failed"
	FinishNoted = "finish-noted"
)

// World is what a pass reads and changes: the tracker, the host's sessions
// and the clock. System gives the real one; tests play it.
type World interface {
	Feature(ref tracker.Ref) (tracker.Feature, error)
	// Task reads one task, and the issue whose operator it @mentions: its
	// feature, or the task itself when it has none.
	Task(ref tracker.Ref) (tracker.Card, tracker.Ref, error)
	// Rows are every claimed task, settled as engine ps settles them.
	Rows() ([]ps.Row, error)
	Windows() ([]session.WindowInfo, error)
	Agents() ([]session.Agent, error)
	LatestRun(name string) (session.Run, bool, error)
	// Slots is how many sessions this host runs at once.
	Slots() (int, error)
	// NoteDir is where the coordinator keeps its own notes for ref.
	NoteDir(ref tracker.Ref) (string, error)
	Start(task tracker.Ref) error
	StartPlanner(feature tracker.Ref) error
	AddLabels(ref tracker.Ref, labels ...string) error
	RemoveLabel(ref tracker.Ref, label string) error
	Comment(ref tracker.Ref, body string) error
	Close(ref tracker.Ref) error
	Operator(ref tracker.Ref) (string, error)
	PullRequests(ref tracker.Ref) ([]string, error)
	Capture(windowID string, lines int) (string, error)
	Kill(windowID string) error
	Now() time.Time
	Logf(format string, args ...any)
}

// Gather reads one pass's View of ref: a feature, or one task when taskOnly.
func Gather(w World, ref tracker.Ref, taskOnly bool) (View, error) {
	v := View{TaskOnly: taskOnly, Now: w.Now(), Operator: ref}
	var err error
	if taskOnly {
		c, op, err := w.Task(ref)
		if err != nil {
			return View{}, err
		}
		v.Feature, v.Operator = tracker.Feature{Ref: ref, Open: true, Tasks: []tracker.Card{c}}, op
	} else if v.Feature, err = w.Feature(ref); err != nil {
		return View{}, err
	}
	if v.StartFailed, err = startFailures(w, v.Feature); err != nil {
		return View{}, err
	}
	if v.Rows, err = rows(w, v.Feature); err != nil {
		return View{}, err
	}
	windows, err := w.Windows()
	if err != nil {
		return View{}, err
	}
	if v.Sessions, err = sessions(w, v.Feature, taskOnly, windows, v.Now); err != nil {
		return View{}, err
	}
	slots, err := w.Slots()
	if err != nil {
		return View{}, err
	}
	v.FreeSlots = slots
	for _, win := range windows {
		if !win.Dead {
			v.FreeSlots--
		}
	}
	if !taskOnly {
		err = lintState(w, &v)
	}
	return v, err
}

// startFailures reads when the last start of each open task, and of the
// feature's planner, failed.
func startFailures(w World, f tracker.Feature) (map[tracker.Ref]time.Time, error) {
	out := map[tracker.Ref]time.Time{}
	refs := []tracker.Ref{f.Ref}
	for _, c := range f.Tasks {
		if c.Open {
			refs = append(refs, c.Ref)
		}
	}
	for _, r := range refs {
		dir, err := w.NoteDir(r)
		if err != nil {
			return nil, err
		}
		at, ok, err := session.ReadNote(dir, StartFailed)
		if err != nil {
			return nil, err
		}
		if ok {
			out[r] = at
		}
	}
	return out, nil
}

func rows(w World, f tracker.Feature) (map[tracker.Ref]ps.Row, error) {
	all, err := w.Rows()
	if err != nil {
		return nil, err
	}
	mine := map[tracker.Ref]bool{}
	for _, c := range f.Tasks {
		mine[c.Ref] = true
	}
	out := map[tracker.Ref]ps.Row{}
	for _, r := range all {
		if ref, err := tracker.ParseRef(r.Task); err == nil && mine[ref] {
			out[ref] = r
		}
	}
	return out, nil
}

// sessions reads the newest run of each task, and of the feature's planner,
// and keeps each one's waiting-since note true to what Claude Code says now.
func sessions(w World, f tracker.Feature, taskOnly bool, windows []session.WindowInfo, now time.Time) (map[tracker.Ref]Session, error) {
	agents, err := w.Agents()
	if err != nil {
		return nil, err
	}
	refs := []tracker.Ref{}
	if !taskOnly {
		refs = append(refs, f.Ref)
	}
	for _, c := range f.Tasks {
		refs = append(refs, c.Ref)
	}
	out := map[tracker.Ref]Session{}
	for _, ref := range refs {
		s, ok, err := readSession(w, ref, windows, agents, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out[ref] = s
		}
	}
	return out, nil
}

func readSession(w World, ref tracker.Ref, windows []session.WindowInfo, agents []session.Agent, now time.Time) (Session, bool, error) {
	repo, err := session.ParseRepo(ref.Repo)
	if err != nil {
		return Session{}, false, err
	}
	name := session.TaskName(repo, ref.Number)
	r, ok, err := w.LatestRun(name)
	if err != nil || !ok {
		return Session{}, false, err
	}
	_, id, _ := strings.Cut(r.Record.Tmux, ":")
	s := Session{Name: name, WindowID: id, RunDir: r.Dir, Mode: r.Record.Mode, Spawned: r.Record.Spawned}
	for _, win := range windows {
		if win.ID == id && win.Name == name {
			s.Live, s.DeadWindow = !win.Dead, win.Dead
		}
	}
	for _, a := range agents {
		if a.Name == name && (a.Status == session.AgentWaiting || a.Status == session.AgentIdle) {
			s.Waiting = true
		}
	}
	if err := keepWaiting(&s, now); err != nil {
		return Session{}, false, err
	}
	s.StallNoted, _, err = session.ReadNote(r.Dir, session.StallNoted)
	return s, true, err
}

// keepWaiting writes the time a session is first seen waiting, and clears it
// when the session works again.
func keepWaiting(s *Session, now time.Time) error {
	if !s.Live || !s.Waiting {
		return session.ClearNote(s.RunDir, session.WaitingSince)
	}
	since, ok, err := session.ReadNote(s.RunDir, session.WaitingSince)
	if err != nil {
		return err
	}
	if !ok {
		since = now
		if err := session.WriteNote(s.RunDir, session.WaitingSince, now); err != nil {
			return err
		}
	}
	s.WaitingSince = since
	return nil
}

// lintState lints an approved feature's plan once its planner is done.
func lintState(w World, v *View) error {
	f := v.Feature
	planner, ok := v.Sessions[f.Ref]
	if !f.Has(Approved) || len(f.Tasks) == 0 || ok && planner.Live {
		return nil
	}
	for _, p := range lint.Lint(f) {
		v.LintProblems = append(v.LintProblems, p.String())
	}
	dir, err := w.NoteDir(f.Ref)
	if err != nil {
		return err
	}
	_, v.LintNoted, err = session.ReadNote(dir, LintNoted)
	return err
}

// Apply does the actions, in order, and reports whether the run is over. A
// failed action is logged, and the pass goes on to the next one.
func Apply(w World, v View, acts []Action) bool {
	over := false
	for _, a := range acts {
		w.Logf("%s %s: %s", a.Kind, a.Ref, logText(a))
		var err error
		switch a.Kind {
		case StartTask:
			err = startTask(w, v, a)
		case StartPlanner:
			err = startTask(w, v, a)
		case SetLabel:
			err = setFeatureLabel(w, v.Feature, a.Label)
		case ReportLint:
			err = reportLint(w, v)
		case Stall:
			err = stall(w, v, a)
		case Fail:
			err = fail(w, v, a)
		case Reap:
			err = w.Kill(a.Session.WindowID)
		case Finish:
			err = finish(w, v)
			over = err == nil
		case Exit:
			over = true
		}
		if err != nil {
			w.Logf("%s %s failed: %v", a.Kind, a.Ref, err)
		}
	}
	return over
}

func logText(a Action) string {
	if a.Label != "" {
		return a.Label
	}
	return a.Text
}

// startTask starts a ready task. A dead window left by its last session is
// closed first, since engine start refuses a name in use. A failed start is
// noted, holds no slot until StartRetry, and @mentions the operator the
// first time after a session start.
func startTask(w World, v View, a Action) error {
	if s, ok := v.Sessions[a.Ref]; ok && s.DeadWindow {
		if err := w.Kill(s.WindowID); err != nil {
			w.Logf("close the dead window of %s: %v", a.Ref, err)
		}
	}
	start := w.Start
	if a.Kind == StartPlanner {
		start = w.StartPlanner
	}
	err := start(a.Ref)
	if err == nil {
		return nil
	}
	dir, derr := w.NoteDir(a.Ref)
	if derr != nil {
		return errors.Join(err, derr)
	}
	// The note is written first, so a failed comment is not posted again by
	// the next try; the mention goes out once per session start.
	prev, seen := v.StartFailed[a.Ref]
	if werr := session.WriteNote(dir, StartFailed, v.Now); werr != nil {
		return errors.Join(err, werr)
	}
	if seen && prev.After(v.Sessions[a.Ref].Spawned) {
		return err
	}
	body := mention(w, v.Operator) + "engine run: starting a session for this issue failed, so it waits " + StartRetry.String() +
		" before the next try:\n\n```text\n" + err.Error() + "\n```"
	return errors.Join(err, w.Comment(a.Ref, body))
}

// setFeatureLabel moves the feature to label, removing its other states.
func setFeatureLabel(w World, f tracker.Feature, label string) error {
	if err := w.AddLabels(f.Ref, label); err != nil {
		return err
	}
	for _, l := range featureStates {
		if l != label && f.Has(l) {
			if err := w.RemoveLabel(f.Ref, l); err != nil {
				return err
			}
		}
	}
	return nil
}

// mention is the @mention of ref's operator, or "" when unknown.
func mention(w World, ref tracker.Ref) string {
	login, err := w.Operator(ref)
	if err != nil || login == "" {
		w.Logf("no operator to mention on %s: %v", ref, err)
		return ""
	}
	return "@" + login + " "
}

func reportLint(w World, v View) error {
	body := mention(w, v.Operator) + "engine run: the plan fails lint, so no task starts:\n\n- " +
		strings.Join(v.LintProblems, "\n- ") + "\n\nFix the cards; the next pass lints again."
	if err := w.Comment(v.Feature.Ref, body); err != nil {
		return err
	}
	dir, err := w.NoteDir(v.Feature.Ref)
	if err != nil {
		return err
	}
	return session.WriteNote(dir, LintNoted, v.Now)
}

// stallLines is how much of a stalled screen a stall comment carries.
const stallLines = 20

func stall(w World, v View, a Action) error {
	body := mention(w, v.Operator) + "engine run: " + a.Text + "."
	if a.Session.Live {
		if screen, err := w.Capture(a.Session.WindowID, stallLines); err == nil && screen != "" {
			body += " Its last screen:\n\n```text\n" + screen + "\n```"
		}
		body += fmt.Sprintf("\n\nAttach with `engine attach %s`.", a.Session.Name)
	}
	if err := w.Comment(a.Ref, body); err != nil {
		return err
	}
	return session.WriteNote(a.Session.RunDir, session.StallNoted, v.Now)
}

func fail(w World, v View, a Action) error {
	var c tracker.Card
	for _, t := range v.Feature.Tasks {
		if t.Ref == a.Ref {
			c = t
		}
	}
	if err := w.AddLabels(a.Ref, Failed); err != nil {
		return err
	}
	for _, l := range append([]string{Merging}, claimStates...) {
		if c.Has(l) {
			if err := w.RemoveLabel(a.Ref, l); err != nil {
				return err
			}
		}
	}
	return w.Comment(a.Ref, mention(w, v.Operator)+"engine run: "+a.Text+", so the task is engine:failed. Its worktree is kept.")
}

// finish posts the feature's summary, marks it done and closes it.
func finish(w World, v View) error {
	f := v.Feature
	var b strings.Builder
	b.WriteString("engine run: every task is closed.\n\n| Task | Pull requests |\n| --- | --- |\n")
	var review []string
	for _, c := range f.Tasks {
		prs, err := w.PullRequests(c.Ref)
		if err != nil {
			w.Logf("pull requests of %s: %v", c.Ref, err)
		}
		fmt.Fprintf(&b, "| %s %s | %s |\n", c.Ref, c.Title, strings.Join(prs, " "))
		if c.Has(Rulings) {
			review = append(review, c.Ref.String())
		}
	}
	for _, c := range f.Tasks {
		if len(f.Tasks) > 1 && isAcceptance(f.Tasks, c) {
			fmt.Fprintf(&b, "\nAcceptance: %s is closed.\n", c.Ref)
		}
	}
	if len(review) > 0 {
		b.WriteString("\nStill carrying engine:ruling-review: " + strings.Join(review, ", ") + ".\n")
	}
	b.WriteString("\nDeviations are in each task's report comment.")
	// A step that fails is retried by the next pass; the note keeps the
	// summary to one post.
	if err := setFeatureLabel(w, f, Done); err != nil {
		return err
	}
	dir, err := w.NoteDir(f.Ref)
	if err != nil {
		return err
	}
	_, posted, err := session.ReadNote(dir, FinishNoted)
	if err != nil {
		return err
	}
	if !posted {
		if err := w.Comment(f.Ref, b.String()); err != nil {
			return err
		}
		if err := session.WriteNote(dir, FinishNoted, v.Now); err != nil {
			return err
		}
	}
	return w.Close(f.Ref)
}
