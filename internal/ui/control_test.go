package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func TestPlanActionsFollowTheStoredState(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		state                 string
		approved, coordinated bool
		want                  []string
	}{
		{"a prepared plan is approved", "prepare", false, false, []string{"approve"}},
		{"approval of a plan with an old approval is still offered", "prepare", true, false, []string{"approve"}},
		{"an approved plan is run", "approved", true, false, []string{"run"}},
		{"a planned plan is run", "planned", true, false, []string{"run"}},
		{"a coordinating plan is stopped", "planned", true, true, []string{"stop"}},
		{"a done plan has none", "done", true, false, []string{}},
		{"a done plan that is still coordinating has none", "done", true, true, []string{}},
		{"an unapproved plan cannot be run", "approved", false, false, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := planActions(tc.state, tc.approved, tc.coordinated); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("planActions(%q, approved %t, coordinating %t) = %v, want %v", tc.state, tc.approved, tc.coordinated, got, tc.want)
			}
		})
	}
}

func TestTaskActionsFollowTheStateAndTheExecution(t *testing.T) {
	for _, tc := range []struct {
		name         string
		state        string
		held, active bool
		want         []string
	}{
		{"a running task with a session is stopped", "running", false, true, []string{"task-stop"}},
		{"an in-review task with a session is stopped", "in-review", false, true, []string{"task-stop"}},
		{"a task at a hard stop is stopped", "needs-human", false, true, []string{"task-stop"}},
		{"a running task whose attempt ended has nothing to stop", "running", false, false, []string{}},
		{"a todo task is completed by hand", "todo", false, false, []string{"complete-external"}},
		{"a todo task being dispatched is left alone", "todo", false, true, []string{}},
		{"a held task is resumed or completed by hand", "todo", true, false, []string{"resume", "complete-external"}},
		{"a failed task is retried or completed by hand", "failed", false, false, []string{"retry", "complete-external"}},
		{"a done task has none", "done", false, false, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskActions(tc.state, tc.held, tc.active); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("taskActions(%q, held %t, active %t) = %v, want %v", tc.state, tc.held, tc.active, got, tc.want)
			}
		})
	}
}

func TestItemControlsCarryTheVersionTheWritesSend(t *testing.T) {
	plan := control.Item{ID: "p1", Kind: "plan", State: "prepare", Version: 4}
	if got := itemControls(plan, true); got.Version != 4 || !reflect.DeepEqual(got.Actions, []string{"approve"}) {
		t.Fatalf("plan controls = %+v", got)
	}
	task := control.Item{ID: "t1", Kind: "task", State: "failed", Version: 7}
	if got := itemControls(task, false); got.Version != 7 || !reflect.DeepEqual(got.Actions, []string{"retry", "complete-external"}) {
		t.Fatalf("task controls = %+v", got)
	}
}

func TestLiveAttempt(t *testing.T) {
	attempts := []control.Attempt{{ID: "a1", State: "ended"}, {ID: "a2", State: "dispatching"}}
	if liveAttempt(attempts, "a1") || !liveAttempt(attempts, "a2") || liveAttempt(attempts, "a3") || liveAttempt(nil, "") {
		t.Fatal("only an attempt that exists and has not ended is live")
	}
}

// The item read asks for attempts only for a todo task with one, and its
// controls say whether an execution is live.
func TestItemRouteOffersControls(t *testing.T) {
	fc := newFakeControl(t)
	items := map[string]control.Item{
		"t1": {ID: "t1", Kind: "task", Repo: "r1", Plan: "p5", Title: "Failed", State: "failed", Version: 3, Attempt: "a40"},
		"t2": {ID: "t2", Kind: "task", Repo: "r1", Plan: "p5", Title: "Held", State: "todo", Held: true, Version: 5, Attempt: "a39"},
		"t3": {ID: "t3", Kind: "task", Repo: "r1", Plan: "p5", Title: "Starting", State: "todo", Version: 2, Attempt: "a40"},
		"p5": {ID: "p5", Kind: "plan", Repo: "r1", Title: "Plan", State: "prepare", Version: 9},
	}
	fc.reads = func(r control.Request) (control.Response, bool) {
		it, ok := items[r.ID]
		return control.Response{Item: &it}, ok
	}
	d := newDashboard(fc.client())
	type offer struct {
		version int
		actions []string
		held    bool
	}
	for _, c := range []struct {
		id   string
		want offer
	}{
		{"t1", offer{3, []string{"retry", "complete-external"}, false}},
		{"t2", offer{5, []string{"resume", "complete-external"}, true}},
		{"t3", offer{2, []string{}, false}},
		{"p5", offer{9, []string{"approve"}, false}},
	} {
		id, want := c.id, c.want
		res, body := d.get(t, "GET", "127.0.0.1:7332", "/api/items/"+id)
		var env struct{ Data ItemDetail }
		if err := json.Unmarshal([]byte(body), &env); err != nil || res.StatusCode != 200 {
			t.Fatalf("GET %s = %d %s %v", id, res.StatusCode, body, err)
		}
		if env.Data.Version != want.version || !reflect.DeepEqual(env.Data.Actions, want.actions) || env.Data.Held != want.held {
			t.Errorf("%s: version %d, actions %v, held %t; want %+v", id, env.Data.Version, env.Data.Actions, env.Data.Held, want)
		}
	}
	if got := strings.Join(fc.calls(), ","); got != "get,get,attempts,get,attempts,get" {
		t.Fatalf("calls = %s: attempts are read only for a todo task with an attempt", got)
	}
}
