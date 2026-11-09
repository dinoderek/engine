package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/ui"
)

// TestDashboardAcceptance runs the real `engine ui` process, from the normal
// command and an engine config under its home, against a control server on
// an isolated store whose session observer reads fake hosts. Each subtest
// proves one of the dashboard's objectives through the page's own routes;
// they run in order, because later ones change the world.
func TestDashboardAcceptance(t *testing.T) {
	s := newControlStack(t, "devbox", "mac")
	w := seedWorld(t, s)
	for name, h := range s.hosts {
		h.set(w.windowsFor(name), nil)
	}
	s.observe()
	d := startDashboard(t, s)
	t.Run("browse plans and tasks", func(t *testing.T) { acceptPlans(t, d, w) })
	t.Run("read descriptions and communications", func(t *testing.T) { acceptDetails(t, d, w) })
	t.Run("see sessions and feedback", func(t *testing.T) { acceptSessions(t, s, d, w) })
	t.Run("survive host and control server outages", func(t *testing.T) { acceptOutages(t, s, d, w) })
}

func check(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

func planIDs(l ui.PlanList) []string {
	var out []string
	for _, p := range l.Plans {
		out = append(out, p.ID)
	}
	return out
}

func acceptPlans(t *testing.T, d *dashboard, w *world) {
	var all ui.PlanList
	status, _ := d.get(t, "/api/plans", &all)
	check(t, status == 200 && all.Total == 3, "plans: %d %+v", status, all)
	check(t, len(all.Repos) == 2 && all.Repos[0].Slug == "owner/alpha" && all.Repos[1].Slug == "owner/beta", "repos: %+v", all.Repos)
	check(t, slices.Equal(all.States, []string{"executing", "prepare"}), "states: %v", all.States)
	check(t, slices.Equal(planIDs(all), []string{w.rollout.ID, w.hardening.ID, w.backup.ID}), "plan order: %v", planIDs(all))
	check(t, all.Plans[0].Slug == "owner/alpha" && all.Plans[0].Title == "Dashboard rollout" && all.Plans[0].State == "executing", "plan row: %+v", all.Plans[0])

	for query, want := range map[string][]string{
		"?repo=" + w.beta.ID + "&state=executing&q=HARD": {w.hardening.ID},
		"?repo=" + w.beta.ID + "&q=rollout":              nil,
		"?q=OWNER/ALPHA":                                 {w.rollout.ID, w.backup.ID},
		"?q=" + strings.ToUpper(w.backup.ID):             {w.backup.ID},
		"?state=prepare&cached=1":                        {w.backup.ID},
	} {
		var got ui.PlanList
		d.get(t, "/api/plans"+query, &got)
		check(t, slices.Equal(planIDs(got), want) && got.Total == 3, "filter %s: %v, want %v", query, planIDs(got), want)
	}

	var rollout ui.PlanDetail
	status, _ = d.get(t, "/api/plans/"+w.rollout.ID, &rollout)
	check(t, status == 200 && rollout.Counts == ui.Counts{Done: 1, InProgress: 2, Ready: 1, Blocked: 2}, "rollout counts: %+v", rollout.Counts)
	rows := map[string]ui.TaskRow{}
	for _, row := range rollout.Tasks {
		rows[row.ID] = row
	}
	for id, want := range map[string]struct{ group, host string }{
		w.schema.ID: {"done", ""}, w.sessionsUI.ID: {"in-progress", "mac"}, w.plansView.ID: {"in-progress", "devbox"},
		w.links.ID: {"blocked", "devbox"}, w.accept.ID: {"blocked", ""}, w.docs.ID: {"ready", ""},
	} {
		check(t, rows[id].Group == want.group && rows[id].Host == want.host, "task %s: %+v, want %+v", id, rows[id], want)
	}
	waiting := rows[w.accept.ID].Waiting
	check(t, len(waiting) == 2 && waiting[0] == ui.Prerequisite{ID: w.sessionsUI.ID, Title: "Sessions UI", State: "running", Plan: w.rollout.ID} &&
		waiting[1] == ui.Prerequisite{ID: w.audit.ID, Title: "Lock audit", State: "todo", Plan: w.backup.ID},
		"acceptance blockers must link both prerequisites, one in another plan: %+v", waiting)
	check(t, len(rows[w.sessionsUI.ID].Waiting) == 0, "a done prerequisite still blocks: %+v", rows[w.sessionsUI.ID].Waiting)
	check(t, rows[w.links.ID].Feedback != nil && rows[w.links.ID].Feedback.Update == w.linksQuestion, "links feedback: %+v", rows[w.links.ID].Feedback)
	check(t, rows[w.plansView.ID].Feedback == nil, "an ordinary wait became feedback: %+v", rows[w.plansView.ID].Feedback)

	var hardening ui.PlanDetail
	d.get(t, "/api/plans/"+w.hardening.ID, &hardening)
	for _, row := range hardening.Tasks {
		// The plan's open question holds up both: the coordinator dispatches
		// no task of a plan whose question is unresolved.
		check(t, row.Feedback != nil && row.Feedback.Item == w.hardening.ID && row.Feedback.Update == w.hardeningQuestion, "%s must link its plan's question: %+v", row.ID, row.Feedback)
	}
	check(t, hardening.Counts == ui.Counts{InProgress: 1, Blocked: 1}, "a queued task under an open plan question is blocked, not ready: %+v", hardening.Tasks)
	status, env := d.get(t, "/api/plans/p99999", nil)
	check(t, status == 404 && env.Error != "", "unknown plan: %d %+v", status, env)
}

func acceptDetails(t *testing.T, d *dashboard, w *world) {
	var plan ui.ItemDetail
	status, _ := d.get(t, "/api/items/"+w.rollout.ID, &plan)
	check(t, status == 200 && plan.Kind == "plan" && plan.Repo == w.alpha.ID && plan.Title == "Dashboard rollout", "plan item: %d %+v", status, plan)
	var types []string
	var text strings.Builder
	links := map[string]string{}
	for _, b := range plan.Description {
		types = append(types, b.Type)
		runs := slices.Concat(append(b.Items, b.Inline)...)
		for _, r := range runs {
			text.WriteString(r.Text)
			if r.Href != "" {
				links[r.Text] = r.Href
			}
		}
		text.WriteString(b.Text)
	}
	check(t, slices.Equal(types, []string{"heading", "paragraph", "list", "paragraph", "code", "paragraph"}), "description blocks: %v", types)
	check(t, links["control server doc"] == "https://example.test/control", "safe link: %v", links)
	check(t, strings.Contains(text.String(), "<script>alert(1)</script>") && strings.Contains(text.String(), "engine ui"), "raw HTML must stay text: %q", text.String())

	var task ui.ItemDetail
	d.get(t, "/api/items/"+w.sessionsUI.ID, &task)
	check(t, task.Kind == "task" && task.Plan == w.rollout.ID && len(task.Description) == 2 && task.Description[1].Type == "list", "task item: %+v", task)

	var first, rest, anchored ui.Thread
	d.get(t, "/api/items/"+w.sessionsUI.ID+"/thread", &first)
	check(t, len(first.Messages) == 50 && first.More && first.Next == first.Messages[49].ID, "first page: %d more=%t", len(first.Messages), first.More)
	d.get(t, fmt.Sprintf("/api/items/%s/thread?after=%d", w.sessionsUI.ID, first.Next), &rest)
	check(t, len(rest.Messages) == progressSays-50 && !rest.More && rest.Messages[len(rest.Messages)-1].ID == w.lastProgress, "second page: %+v", rest)
	d.get(t, fmt.Sprintf("/api/items/%s/thread?anchor=%d", w.sessionsUI.ID, w.lastProgress), &anchored)
	check(t, len(anchored.Messages) == progressSays && anchored.Messages[progressSays-1].ID == w.lastProgress, "a direct link must reach its message: %d", len(anchored.Messages))

	var thread ui.Thread
	d.get(t, "/api/items/"+w.links.ID+"/thread", &thread)
	var kinds []string
	for _, m := range thread.Messages {
		kinds = append(kinds, m.Kind)
		check(t, !strings.HasPrefix(m.Body, "Progress note"), "another item's message leaked in: %+v", m)
	}
	check(t, slices.Equal(kinds, []string{"ask", "acknowledged", "reply"}) && thread.Messages[0].Author == w.attempts[w.links.ID].ID && thread.Messages[2].Author == "operator", "links thread: %+v", thread.Messages)
	check(t, len(thread.Questions) == 1 && thread.Questions[0] == ui.Question{ID: w.linksQuestion, State: "open", Acknowledged: true, Replies: 1}, "question status: %+v", thread.Questions)

	var empty ui.Thread
	d.get(t, "/api/items/"+w.rollout.ID+"/thread", &empty)
	check(t, len(empty.Messages) == 0 && !empty.More, "the planner thread has no messages: %+v", empty)
	status, _ = d.get(t, "/api/items/t99999", nil)
	check(t, status == 404, "unknown item: %d", status)
	status, _ = d.get(t, "/api/items/"+w.links.ID+"/thread?after=-1", nil)
	check(t, status == 400, "bad cursor: %d", status)
}

func sessionMap(l ui.SessionList) map[string]control.SessionRow {
	out := map[string]control.SessionRow{}
	for _, r := range l.Sessions {
		out[r.Item] = r
	}
	return out
}

func acceptSessions(t *testing.T, s *controlStack, d *dashboard, w *world) {
	var list ui.SessionList
	status, _ := d.get(t, "/api/sessions", &list)
	var order []string
	for _, r := range list.Sessions {
		order = append(order, r.Item)
	}
	want := []string{w.links.ID, w.hardening.ID, w.rotate.ID, w.rollout.ID, w.sessionsUI.ID, w.plansView.ID}
	check(t, status == 200 && list.Feedback == 3 && slices.Equal(order, want), "sessions %v (feedback %d), want %v", order, list.Feedback, want)
	rows := sessionMap(list)
	for id, want := range map[string]struct{ role, host, observed string }{
		w.rollout.ID: {"planner", "devbox", "working"}, w.plansView.ID: {"executor", "devbox", "waiting"}, w.links.ID: {"executor", "devbox", "waiting"},
		w.sessionsUI.ID: {"executor", "mac", "working"}, w.hardening.ID: {"planner", "mac", "waiting"}, w.rotate.ID: {"executor", "mac", "live"},
	} {
		r := rows[id]
		check(t, r.Role == want.role && r.Host == want.host && r.Observed == want.observed && r.Harness == "claude" && !r.LastSeen.IsZero() && r.Attempt == w.attempts[id].ID,
			"session %s: %+v, want %+v", id, r, want)
	}
	pv := rows[w.plansView.ID]
	check(t, pv.Feedback == nil && pv.Latest != nil && pv.Latest.Update == w.plansViewSay, "an ordinary wait is not feedback: %+v", pv)
	links := rows[w.links.ID]
	check(t, links.State == "needs-human" && links.Feedback.Update == w.linksQuestion && links.Latest.Update == w.linksReply && links.Latest.Author == "operator",
		"links keeps its question open after an acknowledgement and a reply, and previews the reply: %+v %+v", links.Feedback, links.Latest)
	rotate := rows[w.rotate.ID]
	check(t, rotate.Feedback != nil && rotate.Feedback.Item == w.hardening.ID && rotate.Latest == nil, "a task links its plan's question but previews only its own messages: %+v", rotate)
	build := rows[w.sessionsUI.ID]
	check(t, build.Latest.Update == w.lastProgress && build.Latest.Author == w.attempts[w.sessionsUI.ID].ID && build.Latest.Summary == fmt.Sprintf("Progress note %d", progressSays), "latest: %+v", build.Latest)

	resolution := s.post(t, control.Request{Op: "resolve", ReplyTo: w.linksQuestion, Body: "Yes, link the same feedback"})
	list = readSessions(t, d)
	links = sessionMap(list)[w.links.ID]
	check(t, list.Feedback == 2 && links.Feedback == nil && links.Latest.Update == resolution && links.Latest.Kind == "resolved",
		"a recorded resolution clears the request: %+v", links)
	check(t, links.State == "running" && links.Observed == "waiting", "the resolution restores the task's state, and host state stays: %+v", links)
	check(t, list.Sessions[0].Item == w.hardening.ID, "the remaining requests lead: %+v", list.Sessions[0])
	var rollout ui.PlanDetail
	d.get(t, "/api/plans/"+w.rollout.ID, &rollout)
	for _, row := range rollout.Tasks {
		check(t, row.Feedback == nil, "the task table still links a resolved question: %+v", row)
	}
}

// readSessions reads the Sessions route, which must be fresh.
func readSessions(t *testing.T, d *dashboard) ui.SessionList {
	t.Helper()
	var list ui.SessionList
	if status, env := d.get(t, "/api/sessions", &list); status != 200 || env.Stale {
		t.Fatalf("sessions: %d %+v", status, env)
	}
	return list
}

func (s *controlStack) post(t *testing.T, r control.Request) int {
	t.Helper()
	out, err := s.operator().Call(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return out.Updates[0].ID
}

func acceptOutages(t *testing.T, s *controlStack, d *dashboard, w *world) {
	before := sessionMap(readSessions(t, d))
	s.hosts["devbox"].set(nil, errors.New("ssh: connect to host devbox port 22: Connection refused"))
	s.observe()
	during := sessionMap(readSessions(t, d))
	for _, id := range []string{w.rollout.ID, w.plansView.ID, w.links.ID} {
		check(t, during[id].Observed == "unknown" && during[id].LastSeen.Equal(before[id].LastSeen) && during[id].Host == "devbox",
			"a lost host leaves %s listed, unknown, with its last sighting: %+v", id, during[id])
	}
	check(t, during[w.sessionsUI.ID].Observed == "working" && during[w.sessionsUI.ID].LastSeen.After(before[w.sessionsUI.ID].LastSeen), "the other host is unaffected: %+v", during[w.sessionsUI.ID])

	var plans ui.PlanList
	var plan ui.PlanDetail
	d.get(t, "/api/plans", &plans)
	d.get(t, "/api/plans/"+w.rollout.ID, &plan)
	s.stop()
	var stalePlans ui.PlanList
	var stalePlan ui.PlanDetail
	var staleSessions ui.SessionList
	for path, out := range map[string]any{"/api/plans": &stalePlans, "/api/plans/" + w.rollout.ID: &stalePlan, "/api/sessions": &staleSessions} {
		status, env := d.get(t, path, out)
		check(t, status == 200 && env.Stale && env.Error == "control server unreachable" && !env.Fetched.IsZero(),
			"%s during the interruption keeps its last data, stale: %d %+v", path, status, env.Error)
	}
	check(t, slices.Equal(planIDs(stalePlans), planIDs(plans)) && stalePlan.Counts == plan.Counts && len(staleSessions.Sessions) == len(during),
		"stale views must be the last successful ones: %v %+v %d", planIDs(stalePlans), stalePlan.Counts, len(staleSessions.Sessions))
	var cached ui.PlanList
	status, env := d.get(t, "/api/plans?cached=1&q=hard", &cached)
	check(t, status == 200 && env.Stale && slices.Equal(planIDs(cached), []string{w.hardening.ID}), "filtering the last list: %d %+v %v", status, env, planIDs(cached))
	for _, path := range []string{"/api/items/" + w.links.ID, "/api/items/" + w.links.ID + "/thread"} {
		status, env := d.get(t, path, nil)
		check(t, status == 502 && env.Error == "control server unreachable", "%s: %d %+v; the page keeps the open panel's data", path, status, env)
	}

	s.start()
	restarted := sessionMap(readSessions(t, d))
	check(t, restarted[w.sessionsUI.ID].Observed == "unknown" && restarted[w.sessionsUI.ID].LastSeen.IsZero(), "a restarted server has observed nothing yet: %+v", restarted[w.sessionsUI.ID])
	s.hosts["devbox"].set(w.windowsFor("devbox"), nil)
	s.observe()
	recovered := sessionMap(readSessions(t, d))
	for id, r := range recovered {
		check(t, r.Observed != "unknown" && !r.LastSeen.IsZero(), "after recovery %s is observed again: %+v", id, r)
	}
	var thread ui.Thread
	status, env = d.get(t, "/api/items/"+w.links.ID+"/thread", &thread)
	check(t, status == 200 && !env.Stale && len(thread.Messages) == 4 && thread.Questions[0].State == "resolved", "the open panel's thread recovers: %d %+v", status, thread)
	status, env = d.get(t, "/api/plans", &plans)
	check(t, status == 200 && !env.Stale && env.Error == "" && plans.Total == 3, "plans recover: %d %+v", status, env)
}
