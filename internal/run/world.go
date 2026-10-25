package run

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The coordinator's own notes: that it has reported the plan's lint
// failures, when a task's start last failed, and that it has posted the
// summary.
const (
	LintNoted   = "lint-noted"
	StartFailed = "start-failed"
	FinishNoted = "finish-noted"
)

// World is what a pass reads and changes: the tracker, the hosts' sessions
// and the clock. System gives the real one; tests play it. Every host is
// read through Fleet, one status call each, and changed through Start and
// Release.
type World interface {
	Feature(ref tracker.Ref) (tracker.Feature, error)
	// Task reads one task, and the issue whose operator it @mentions: its
	// feature, or the task itself when it has none.
	Task(ref tracker.Ref) (tracker.Card, tracker.Ref, error)
	// Rows are every claimed task, settled as engine ps settles them, from
	// the hosts' replies in f.
	Rows(f Fleet) ([]ps.Row, error)
	// Fleet reads every host's status. A host that cannot be read is in it,
	// marked unreachable.
	Fleet() Fleet
	// NoteDir is where the coordinator keeps its own notes for ref.
	NoteDir(ref tracker.Ref) (string, error)
	// Start starts the task's session on the named host.
	Start(task tracker.Ref, host string) error
	StartPlanner(feature tracker.Ref) error
	AddLabels(ref tracker.Ref, labels ...string) error
	RemoveLabel(ref tracker.Ref, label string) error
	Comment(ref tracker.Ref, body string) error
	Close(ref tracker.Ref) error
	Operator(ref tracker.Ref) (string, error)
	PullRequests(ref tracker.Ref) ([]string, error)
	// Release closes the session's window on host, and removes its
	// worktree unless it has changes.
	Release(host, name string) (hostops.Released, error)
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
	if v.StartFailed, v.PlaceNoted, err = taskNotes(w, v.Feature); err != nil {
		return View{}, err
	}
	v.Fleet = w.Fleet()
	if v.Rows, err = rows(w, v.Feature, v.Fleet); err != nil {
		return View{}, err
	}
	if v.Sessions, err = sessions(w, v); err != nil {
		return View{}, err
	}
	if !taskOnly {
		err = lintState(w, &v)
	}
	return v, err
}

// taskNotes reads when the last start of each open task, and of the
// feature's planner, failed, and which tasks have been said to fit no host.
func taskNotes(w World, f tracker.Feature) (failed map[tracker.Ref]time.Time, placeNoted map[tracker.Ref]bool, err error) {
	failed, placeNoted = map[tracker.Ref]time.Time{}, map[tracker.Ref]bool{}
	refs := []tracker.Ref{f.Ref}
	for _, c := range f.Tasks {
		if c.Open {
			refs = append(refs, c.Ref)
		}
	}
	for _, r := range refs {
		dir, err := w.NoteDir(r)
		if err != nil {
			return nil, nil, err
		}
		at, ok, err := session.ReadNote(dir, StartFailed)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			failed[r] = at
		}
		if _, placeNoted[r], err = session.ReadNote(dir, PlaceNoted); err != nil {
			return nil, nil, err
		}
	}
	return failed, placeNoted, nil
}

func rows(w World, f tracker.Feature, fleet Fleet) (map[tracker.Ref]ps.Row, error) {
	all, err := w.Rows(fleet)
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
		case Wait:
			err = placeComment(w, v, a)
		case SetLabel:
			err = setFeatureLabel(w, v.Feature, a.Label)
		case ReportLint:
			err = reportLint(w, v)
		case Stall:
			err = stall(w, v, a)
		case Fail:
			err = fail(w, v, a)
		case Reap:
			err = release(w, a.Session)
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

// startTask starts a ready task. The last session, which has ended, is
// released on its host first: engine start refuses a name in use, and a
// worktree left on the task's branch.
// A started session is noted with its host, so a host that cannot be read
// leaves it unknown. A failed start is noted, holds no slot until
// StartRetry, and @mentions the operator the first time after a session
// start.
func startTask(w World, v View, a Action) error {
	if s, ok := v.Sessions[a.Ref]; ok && !s.Live && !s.Unknown {
		if err := release(w, s); err != nil {
			w.Logf("release the last session of %s on %s: %v", a.Ref, s.Host, err)
		}
	}
	start, host := func(r tracker.Ref) error { return w.Start(r, a.Host) }, a.Host
	if a.Kind == StartPlanner {
		start, host = w.StartPlanner, v.Fleet.Local
	}
	err := start(a.Ref)
	dir, derr := w.NoteDir(a.Ref)
	if derr != nil {
		return errors.Join(err, derr)
	}
	if err == nil {
		return session.WriteNote(dir, StartedOn+host, v.Now)
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

// placeComment says on a task, once, that no host can take it, and why.
func placeComment(w World, v View, a Action) error {
	if !a.Notify {
		return nil
	}
	body := mention(w, v.Operator) + "engine run: " + a.Text + ". The task stays ready; it starts once hosts.toml or its card lets a host take it."
	if err := w.Comment(a.Ref, body); err != nil {
		return err
	}
	dir, err := w.NoteDir(a.Ref)
	if err != nil {
		return err
	}
	return session.WriteNote(dir, PlaceNoted, v.Now)
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

// stall comments on a stalled session, with the last screen its host sent.
func stall(w World, v View, a Action) error {
	body := mention(w, v.Operator) + "engine run: " + a.Text + "."
	if screen := strings.TrimRight(a.Session.Screen, "\n"); screen != "" && (a.Session.Live || a.Session.DeadWindow) {
		body += " Its last screen:\n\n```text\n" + screen + "\n```"
	}
	if a.Session.Live {
		body += fmt.Sprintf("\n\nAttach with `engine attach %s`.", a.Session.Name)
	}
	if err := w.Comment(a.Ref, body); err != nil {
		return err
	}
	dir, err := w.NoteDir(a.Ref)
	if err != nil {
		return err
	}
	return session.WriteNote(dir, StallNoted, a.At)
}

// release releases a session on its host, and logs a worktree it kept.
func release(w World, s Session) error {
	r, err := w.Release(s.Host, s.Name)
	if err == nil && r.WorktreeKept {
		w.Logf("release %s on %s kept its worktree %s: %s", s.Name, s.Host, r.Worktree, r.Note)
	}
	return err
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
