package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func sessionRows(t *testing.T, s Service) map[string]SessionRow {
	t.Helper()
	rows := map[string]SessionRow{}
	for _, row := range call(t, s, op, Request{Op: "session-view"}).Sessions {
		rows[row.Item] = row
	}
	return rows
}

func TestSessionViewListsCurrentPlannerAndExecutorAttempts(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	child, replaced, ended := newTask(t, s, repo.ID, plan.ID), newTask(t, s, repo.ID, plan.ID), newTask(t, s, repo.ID, plan.ID)
	plan = approve(t, s, plan)
	standalone := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Title: "Standalone", Body: "## Outcome\nWorks"}}).Item
	standalone = approve(t, s, standalone)
	seen := time.Date(2026, 11, 7, 8, 59, 0, 0, time.UTC)
	observed := map[string]Observation{}
	s.Observe = func(id string) Observation { return observed[id] }
	worker, planner := dispatch(t, s, plan)
	_, childRun := dispatch(t, s, child)
	_, standaloneRun := dispatch(t, s, standalone)
	_, old := dispatch(t, s, replaced)
	_, gone := dispatch(t, s, ended)
	for _, id := range []string{old.Attempt.ID, gone.Attempt.ID} {
		if err := s.SetAttempt(id, "ended", ""); err != nil {
			t.Fatal(err)
		}
	}
	_, current := dispatch(t, s, replaced)
	observed[childRun.Attempt.ID] = Observation{State: "waiting", Seen: seen}
	observed[standaloneRun.Attempt.ID] = Observation{State: "exited", Seen: seen}
	observed[old.Attempt.ID] = Observation{State: "working", Seen: seen}

	reject(t, s, worker, Request{Op: "session-view"}, 403)
	out := call(t, s, op, Request{Op: "session-view"})
	order := []string{}
	for _, row := range out.Sessions {
		order = append(order, row.Item)
	}
	if want := []string{plan.ID, child.ID, replaced.ID, standalone.ID}; !slices.Equal(order, want) {
		t.Fatalf("rows %v, want %v", order, want)
	}
	rows := sessionRows(t, s)
	if p := rows[plan.ID]; p.Role != "planner" || p.Kind != "plan" || p.Plan != plan.ID || p.Attempt != planner.Attempt.ID || p.Observed != "unknown" || !p.LastSeen.IsZero() {
		t.Fatalf("planner row: %+v", p)
	}
	if c := rows[child.ID]; c.Role != "executor" || c.Plan != plan.ID || c.Observed != "waiting" || !c.LastSeen.Equal(seen) ||
		c.Host != "devbox" || c.Harness != "claude" || c.RepoSlug != "owner/demo" || c.Repo != repo.ID || c.State != "running" || c.Title != "Build" {
		t.Fatalf("child row: %+v", c)
	}
	if r := rows[replaced.ID]; r.Attempt != current.Attempt.ID || r.Observed != "unknown" || !r.LastSeen.IsZero() {
		t.Fatalf("replaced attempt leaked its observation: %+v", r)
	}
	if a := rows[standalone.ID]; a.Plan != "" || a.Observed != "exited" || !a.LastSeen.Equal(seen) {
		t.Fatalf("confirmed exit must stay visible until the attempt ends: %+v", a)
	}
	s.Observe = nil
	if c := sessionRows(t, s)[child.ID]; c.Observed != "unknown" || !c.LastSeen.IsZero() {
		t.Fatalf("missing observer: %+v", c)
	}
}

func TestSessionViewFeedbackAndLatestItemMessage(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	plan = approve(t, s, plan)
	planner, _ := dispatch(t, s, plan)
	worker, claimed := dispatch(t, s, task)
	if r := sessionRows(t, s)[task.ID]; r.Feedback != nil || r.Latest != nil {
		t.Fatalf("lifecycle entries became messages: %+v", r)
	}
	call(t, s, worker, Request{Op: "say", ID: task.ID, Body: "Started\n\n  the   build"})
	if l := sessionRows(t, s)[task.ID].Latest; l == nil || l.Summary != "Started the build" || l.Author != claimed.Attempt.ID || l.Kind != "say" || l.Item != task.ID {
		t.Fatalf("latest message: %+v", l)
	}
	question := call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve the API change?"}).Updates[0]
	call(t, s, op, Request{Op: "ack", ReplyTo: question.ID})
	row := sessionRows(t, s)[task.ID]
	if row.Feedback == nil || row.Feedback.Update != question.ID || row.Feedback.Item != task.ID || row.Latest.Update != question.ID || row.State != "needs-human" {
		t.Fatalf("acknowledgement closed or replaced the question: %+v", row)
	}
	reply := call(t, s, op, Request{Op: "reply", ReplyTo: question.ID, Body: "Investigating"}).Updates[0]
	row = sessionRows(t, s)[task.ID]
	if row.Feedback == nil || row.Feedback.Update != question.ID || row.Latest.Update != reply.ID || row.Latest.Author != "operator" {
		t.Fatalf("a reply closed the question: %+v", row)
	}
	resolution := call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved"}).Updates[0]
	row = sessionRows(t, s)[task.ID]
	if row.Feedback != nil || row.Latest.Update != resolution.ID || row.Latest.Kind != "resolved" {
		t.Fatalf("recorded resolution: %+v", row)
	}
	call(t, s, Actor{Name: "coordinator", Role: "coordinator"}, Request{Op: "say", ID: task.ID, Body: "Session waited ten minutes", Recipient: "operator"})
	if l := sessionRows(t, s)[task.ID].Latest; l.Update != resolution.ID {
		t.Fatalf("an automatic notice became the preview: %+v", l)
	}

	parent := call(t, s, planner, Request{Op: "ask", ID: plan.ID, Body: "May the plan change scope?"}).Updates[0]
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "Plan-level note", Recipient: "task"})
	rows := sessionRows(t, s)
	if f := rows[task.ID].Feedback; f == nil || f.Item != plan.ID || f.Update != parent.ID || f.Summary != "May the plan change scope?" {
		t.Fatalf("parent question missing from the task row: %+v", f)
	}
	if l := rows[task.ID].Latest; l.Update != resolution.ID {
		t.Fatalf("task message must stay scoped to the task: %+v", l)
	}
	if p := rows[plan.ID]; p.Feedback == nil || p.Feedback.Update != parent.ID || p.Latest.Summary != "Plan-level note" {
		t.Fatalf("planner row: %+v", p)
	}
}

func TestSessionViewResponseHoldsOnlyAllowlistedFields(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, claimed := dispatch(t, s, task)
	lock := call(t, s, worker, Request{Op: "lock-acquire", Attempt: claimed.Attempt.ID}).Lock
	call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Question"})
	s.Observe = func(string) Observation {
		return Observation{State: "working", Seen: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	}
	b, err := json.Marshal(call(t, s, op, Request{Op: "session-view"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{claimed.Credential, lock.Token, claimed.Attempt.RunID, "Agreed requirements", "## Outcome"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("session view exposed %q: %s", secret, b)
		}
	}
	var out map[string][]map[string]any
	if err = json.Unmarshal(b, &out); err != nil || len(out) != 1 || len(out["sessions"]) != 1 {
		t.Fatalf("top-level fields: %s", b)
	}
	rowFields := []string{"item", "kind", "role", "title", "repo", "repo_slug", "plan", "state", "attempt", "host", "harness", "started", "observed", "last_seen", "feedback", "latest"}
	refFields := []string{"item", "update", "kind", "author", "at", "summary"}
	row := out["sessions"][0]
	assertFields(t, row, rowFields)
	for _, ref := range []string{"feedback", "latest"} {
		nested, ok := row[ref].(map[string]any)
		if !ok {
			t.Fatalf("%s missing: %s", ref, b)
		}
		assertFields(t, nested, refFields)
	}
}

func assertFields(t *testing.T, got map[string]any, allowed []string) {
	t.Helper()
	for key := range got {
		if !slices.Contains(allowed, key) {
			t.Fatalf("field %q is outside the allowlist", key)
		}
	}
	if len(got) != len(allowed) {
		t.Fatalf("fields %v, want %v", got, allowed)
	}
}

func TestSessionViewHTTPRequiresTheOperator(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, claimed := dispatch(t, s, task)
	token := Secret()
	server := httptest.NewServer(Handler(s, token, t.Logf))
	defer server.Close()
	out, err := Client{URL: server.URL, Token: token}.Call(context.Background(), Request{Op: "session-view"})
	if err != nil || len(out.Sessions) != 1 || out.Sessions[0].Item != task.ID {
		t.Fatalf("operator session view: %+v %v", out, err)
	}
	_, err = Client{URL: server.URL, Token: claimed.Credential}.Call(context.Background(), Request{Op: "session-view"})
	var p *Problem
	if !errors.As(err, &p) || p.Status != http.StatusForbidden {
		t.Fatalf("worker session view: %v", err)
	}
}

func TestSummaryIsOneBoundedLine(t *testing.T) {
	long := strings.Repeat("word ", SummaryRunes)
	cases := []struct{ body, want string }{
		{"", ""},
		{" one\n\ttwo  ", "one two"},
		{strings.Repeat("é", SummaryRunes), strings.Repeat("é", SummaryRunes)},
		{strings.Repeat("é", SummaryRunes+1), strings.Repeat("é", SummaryRunes-1) + "…"},
	}
	for _, c := range cases {
		if got := Summary(c.body); got != c.want {
			t.Errorf("Summary(%q) = %q, want %q", c.body, got, c.want)
		}
	}
	if got := []rune(Summary(long)); len(got) > SummaryRunes || got[len(got)-1] != '…' {
		t.Fatalf("long summary: %q", string(got))
	}
}
