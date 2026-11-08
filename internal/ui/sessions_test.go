package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

func ref(item string, update int) *control.UpdateRef {
	return &control.UpdateRef{Item: item, Update: update, Kind: "ask", Author: "t28", At: time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), Summary: "Approve?"}
}

// fixtureSessions: t28 asks its own question, t29 inherits the plan's older
// one, t5 is a standalone task waiting without a question, p24's planner has
// a resolved (so absent) question, and t9's host is gone.
func fixtureSessions() []control.SessionRow {
	return []control.SessionRow{
		{Item: "p24", Kind: "plan", Role: "planner", Plan: "p24", Observed: "waiting", Host: "mac", Harness: "claude"},
		{Item: "t5", Kind: "task", Role: "executor", Observed: "waiting", Host: "mac", Harness: "codex"},
		{Item: "t9", Kind: "task", Role: "executor", Plan: "p24", Observed: "unknown", Host: "devbox", Harness: "claude"},
		{Item: "t28", Kind: "task", Role: "executor", Plan: "p24", Observed: "working", Host: "devbox", Harness: "claude", Feedback: ref("t28", 40)},
		{Item: "t29", Kind: "task", Role: "executor", Plan: "p24", Observed: "exited", Host: "mac", Harness: "claude", Feedback: ref("p24", 12)},
	}
}

func rowIDs(rows []control.SessionRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Item)
	}
	return out
}

func TestSessionListPutsFeedbackFirst(t *testing.T) {
	in := fixtureSessions()
	got := sessionList(in)
	if want := []string{"t29", "t28", "t5", "t9", "p24"}; !reflect.DeepEqual(rowIDs(got.Sessions), want) {
		t.Fatalf("order = %v, want the oldest request first, then item numbers: %v", rowIDs(got.Sessions), want)
	}
	if got.Feedback != 2 {
		t.Fatalf("feedback = %d, want two requests: waiting and unreachable sessions are not requests", got.Feedback)
	}
	if rowIDs(in)[0] != "p24" {
		t.Fatalf("input reordered: %v", rowIDs(in))
	}
	same := sessionList([]control.SessionRow{{Item: "t12", Feedback: ref("t12", 3)}, {Item: "t3", Feedback: ref("t3", 3)}})
	if want := []string{"t3", "t12"}; !reflect.DeepEqual(rowIDs(same.Sessions), want) {
		t.Fatalf("tie = %v, want item order %v", rowIDs(same.Sessions), want)
	}
}

func TestSessionListEmpty(t *testing.T) {
	b, err := json.Marshal(sessionList(nil))
	if err != nil || string(b) != `{"sessions":[],"feedback":0}` {
		t.Fatalf("empty list = %s, %v, want an empty array", b, err)
	}
}

func TestPlanDetailCarriesApplicableFeedback(t *testing.T) {
	rows := []control.SessionRow{
		{Item: "t28", Feedback: ref("p24", 12)},
		{Item: "t29", Observed: "waiting"},
		{Item: "t33", Feedback: ref("t33", 50)},
	}
	got := planDetail("p24", fixtureView(), fixtureAttempts(), rows)
	byID := map[string]TaskRow{}
	for _, tk := range got.Tasks {
		byID[tk.ID] = tk
	}
	if f := byID["t28"].Feedback; f == nil || f.Item != "p24" || f.Update != 12 {
		t.Fatalf("t28 feedback = %+v, want the plan's question", f)
	}
	if byID["t29"].Feedback != nil {
		t.Fatalf("a waiting session shows feedback: %+v", byID["t29"])
	}
	if f := byID["t33"].Feedback; f == nil || f.Item != "t33" || byID["t33"].Group != groupBlocked || byID["t28"].Group != groupInProgress {
		t.Fatalf("feedback changed an active or failed task's group: %+v", got.Tasks)
	}
	// The plan's open question holds up every task waiting to start: the
	// server dispatches none of them until it is resolved.
	for _, id := range []string{"t30", "t31", "t32"} {
		if f := byID[id].Feedback; f == nil || f.Item != "p24" || f.Update != 12 || byID[id].Group != groupBlocked {
			t.Fatalf("%s under an open plan question = %+v, want blocked with the plan's question", id, byID[id])
		}
	}
	failedAlone := planDetail("p24", fixtureView(), fixtureAttempts(), rows[:1])
	for _, tk := range failedAlone.Tasks {
		if tk.ID == "t33" && (tk.Feedback != nil || tk.Group != groupBlocked) {
			t.Fatalf("a failed task is not waiting to start, so does not link the plan's question: %+v", tk)
		}
	}
	if byID["t26"].Feedback != nil || got.Counts != (Counts{Done: 1, InProgress: 2, Blocked: 4}) {
		t.Fatalf("counts under an open plan question = %+v, done task %+v", got.Counts, byID["t26"])
	}
	without := planDetail("p24", fixtureView(), fixtureAttempts(), rows[1:])
	if without.Counts != (Counts{Done: 1, InProgress: 2, Ready: 1, Blocked: 3}) {
		t.Fatalf("a task's own question must not hold up its siblings: %+v", without.Counts)
	}
}

func TestSessionsRouteOrdersAndKeepsLastDataDuringOutage(t *testing.T) {
	fc := newFakeControl(t)
	fc.rows = fixtureSessions()
	d := newDashboard(fc.client())
	res, body := d.get(t, "GET", "127.0.0.1:7332", "/api/sessions")
	env := decode(t, body)
	if res.StatusCode != 200 || env.Stale || !strings.Contains(body, `"feedback":2`) || strings.Index(body, `"item":"t29"`) > strings.Index(body, `"item":"p24"`) {
		t.Fatalf("sessions = %d %s, want feedback rows first", res.StatusCode, body)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	fc.failWith(500)
	res, body = d.get(t, "GET", "127.0.0.1", "/api/sessions")
	env = decode(t, body)
	if res.StatusCode != 200 || !env.Stale || env.Error == "" || !strings.Contains(body, `"item":"t29"`) {
		t.Fatalf("during an outage = %d %s, want the last data marked stale", res.StatusCode, body)
	}
	fc.failWith(0)
	if _, body = d.get(t, "GET", "127.0.0.1", "/api/sessions"); decode(t, body).Stale {
		t.Fatalf("recovered sessions = %s, want fresh", body)
	}
}

func TestSessionsRouteWithoutDataReportsTheFailure(t *testing.T) {
	fc := newFakeControl(t)
	fc.failWith(500)
	res, body := newDashboard(fc.client()).get(t, "GET", "127.0.0.1", "/api/sessions")
	if res.StatusCode != 502 || decode(t, body).Error == "" {
		t.Fatalf("first load during an outage = %d %s, want 502 with a message", res.StatusCode, body)
	}
}

func TestSessionsRouteRefusesNonReads(t *testing.T) {
	d := newDashboard(newFakeControl(t).client())
	if res, _ := d.get(t, "POST", "127.0.0.1", "/api/sessions"); res.StatusCode != 405 {
		t.Fatalf("POST = %d, want 405", res.StatusCode)
	}
}

func TestPlanRouteCarriesFeedbackFromTheSessionView(t *testing.T) {
	fc := newFakeControl(t)
	fc.rows = fixtureSessions()
	fc.rows[3].Item = "t28"
	d := newDashboard(fc.client())
	_, body := d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	if !strings.Contains(body, `"feedback":{"item":"t28","update":40`) {
		t.Fatalf("plan = %s, want the task summary to carry its feedback", body)
	}
	fc.failWith(500)
	_, body = d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	if env := decode(t, body); !env.Stale || !strings.Contains(body, `"update":40`) {
		t.Fatalf("plan during an outage = %s, want the last view with feedback, stale", body)
	}
}
