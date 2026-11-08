package controlrun

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

func reference(st control.State, item control.Item) tracker.Ref {
	return tracker.Ref{Repo: st.Items[item.Repo].Slug, Number: control.Number(item.ID)}
}

func find(st control.State, ref tracker.Ref) (control.Item, bool) {
	for _, item := range st.Items {
		if item.Kind != "repo" && reference(st, item) == ref {
			return item, true
		}
	}
	return control.Item{}, false
}

func card(st control.State, item control.Item) tracker.Card {
	body := executionBody(item)
	out := tracker.Card{Ref: reference(st, item), Title: item.Title, Body: body, Open: item.State != "done", Requires: item.Requires, Labels: labels(item)}
	for _, id := range item.Depends {
		dep := st.Items[id]
		out.BlockedBy = append(out.BlockedBy, tracker.Blocker{Ref: reference(st, dep), Open: dep.State != "done"})
	}
	return out
}

func executionBody(item control.Item) string {
	var lines []string
	skip := false
	for _, line := range strings.Split(item.Body, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			skip = heading == "Requires" || heading == "Harness / tier"
		}
		if !skip {
			lines = append(lines, line)
		}
	}
	body := strings.Join(lines, "\n") + "\n<!-- engine:card " + item.ID + " -->\n## Requires\n" + strings.Join(item.Requires, ", ") + "\n"
	if item.Harness != "" || item.Tier != "" {
		body += "\n## Harness / tier\n" + strings.TrimSpace(item.Harness+" "+item.Tier) + "\n"
	}
	return body
}

func labels(item control.Item) []string {
	if item.State == "todo" || item.State == "done" {
		return nil
	}
	return []string{"engine:" + item.State}
}

func (c *Coordinator) view(st control.State, id string, fleet run.Fleet) (run.View, error) {
	item := st.Items[id]
	now := time.Now().UTC()
	if c.Service.Now != nil {
		now = c.Service.Now()
	}
	v := run.View{TaskOnly: item.Kind == "task", Now: now, Fleet: fleet, Rows: map[tracker.Ref]ps.Row{}, Sessions: map[tracker.Ref]run.Session{},
		StartFailed: map[tracker.Ref]time.Time{}, PlaceNoted: map[tracker.Ref]bool{}}
	v.Feature = Feature(st, id)
	items := featureItems(st, id)
	c.addSessions(st, &v, append(items, item))
	if item.Kind == "plan" && len(v.Feature.Tasks) > 0 {
		for _, p := range lint.Lint(v.Feature) {
			v.LintProblems = append(v.LintProblems, p.String())
		}
	}
	return v, c.keepObservations(st, v)
}

// Feature builds the same plan projection for linting and coordination.
// It only reads the snapshot; it never observes hosts or persists state.
func Feature(st control.State, id string) tracker.Feature {
	item := st.Items[id]
	f := tracker.Feature{Ref: reference(st, item), Title: item.Title, Open: item.State != "done", Labels: labels(item)}
	for _, t := range featureItems(st, id) {
		f.Tasks = append(f.Tasks, card(st, t))
	}
	return f
}

func featureItems(st control.State, id string) []control.Item {
	var items []control.Item
	for _, t := range st.Items {
		if t.Plan == id || t.ID == id && t.Kind == "task" {
			items = append(items, t)
		}
	}
	slices.SortFunc(items, func(a, b control.Item) int { return control.Number(a.ID) - control.Number(b.ID) })
	return items
}

func (c *Coordinator) addSessions(st control.State, v *run.View, items []control.Item) {
	for _, t := range items {
		at, ok := st.Attempts[t.Attempt]
		if !ok {
			continue
		}
		ref := reference(st, t)
		if control.Vanished(&st, t) {
			resumeVanished(&st, v, t, ref)
			continue
		}
		sess := observe(at, v.Fleet, st.Items[t.Repo].Slug, control.Number(t.ID))
		sess.WaitingSince = at.WaitingSince
		sess.StallNoted = at.Notice
		if sess.ResetsAt == 0 && sess.Waiting {
			sess.ResetsAt = at.ResetsAt
		}
		if at.ResumedFor == sess.ResetsAt {
			sess.ResumeAt = at.ResumeAt
		}
		sess.FarNoted = at.FarFor == sess.ResetsAt && sess.ResetsAt > 0
		sess.StuckNoted = at.StuckFor == sess.ResetsAt && sess.ResetsAt > 0
		v.Sessions[ref] = sess
		if !v.TaskOnly && t.Kind == "plan" {
			continue
		}
		if slices.Contains([]string{"running", "needs-human", "in-review"}, t.State) {
			state := t.State
			if !sess.Live && !sess.Unknown {
				state = ps.Dead
			}
			v.Rows[ref] = ps.Row{Task: ref.String(), Session: sess.Name, Host: at.Host, State: state}
		}
	}
}

// resumeVanished shows a task whose attempt vanished as ready to start, so
// it is dispatched again as a resume, unless an operator question parks it.
// Its ended attempt has no session to observe.
func resumeVanished(st *control.State, v *run.View, t control.Item, ref tracker.Ref) {
	if control.PendingQuestion(st, t.ID) > 0 {
		return
	}
	for i, c := range v.Feature.Tasks {
		if c.Ref == ref {
			v.Feature.Tasks[i].Labels = nil
		}
	}
}

func observe(at control.Attempt, fleet run.Fleet, slug string, number int) run.Session {
	s := run.Session{Name: sessionName(slug, number), Host: at.Host, WindowID: at.Window, Spawned: at.Started, Mode: "hands-off"}
	for _, h := range fleet.Hosts {
		if h.Name != at.Host {
			continue
		}
		if h.Unreachable != "" || h.Status.RunsError != "" {
			s.Unknown = true
			return s
		}
		if w, ok := matchWindow(at, h.Status, s.Name); ok {
			s.Live = w.Alive
			s.DeadWindow = !w.Alive
			s.Waiting = w.Alive && w.State == session.StateWaiting
			s.Limited = w.Alive && w.State == session.StateLimited
			s.ResetsAt = w.ResetsAt
			s.Screen = w.Screen
			return s
		}
		s.Unknown = at.State == "dispatching"
		return s
	}
	s.Unknown = true
	return s
}

func (c *Coordinator) keepObservations(st control.State, v run.View) error {
	return c.Service.Store.Transaction(func(fresh *control.State) error {
		for ref, sess := range v.Sessions {
			item, ok := find(st, ref)
			if !ok {
				continue
			}
			at := fresh.Attempts[item.Attempt]
			if at.ID == "" || sess.Unknown {
				continue
			}
			at = kept(at, sess, v.Now)
			fresh.Attempts[at.ID] = at
		}
		return nil
	})
}

// kept is at with one pass's observation of its session applied.
func kept(at control.Attempt, sess run.Session, now time.Time) control.Attempt {
	if sess.Waiting && !sess.Limited && sess.ResetsAt == 0 {
		if at.WaitingSince.IsZero() {
			at.WaitingSince = now
		}
	} else {
		at.WaitingSince = time.Time{}
	}
	if sess.ResetsAt > 0 {
		at.ResetsAt = sess.ResetsAt
	}
	if sess.Live {
		at.LiveAt = now
	}
	if sess.Live && !sess.Waiting && !sess.Limited {
		at.ResetsAt = 0
	}
	return at
}

func (c *Coordinator) summary(st control.State, id string) string {
	text := "Every task is complete."
	for _, t := range st.Items {
		if t.Plan == id {
			text += fmt.Sprintf("\n%s: %s %s", t.ID, t.Title, t.PR)
		}
	}
	return text
}
