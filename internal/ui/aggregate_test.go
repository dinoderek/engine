package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// aggregateServer is a control server that has overview and plan-detail. The
// inbox holds one question on t8, and updates pages come from updates.
func aggregateServer(updates []control.Update) *recorder {
	tasks, _ := inboxItems()
	return &recorder{aggregates: true, reply: func(r control.Request) (control.Response, error) {
		switch r.Op {
		case "overview":
			items := append(append(fixtureRepos(), fixturePlans()...), tasks[:2]...)
			return control.Response{Items: items, Sessions: fixtureSessions(),
				Updates: []control.Update{inboxUpdate(5, "ask", "t8", "Merge it?", questioning)}}, nil
		case "plan-detail":
			return control.Response{Items: fixtureView(), Attempts: fixtureAttempts(), Sessions: fixtureSessions()}, nil
		case "updates":
			out := control.Response{Questions: []control.QuestionStatus{{ID: 1, State: "open", Replies: 2}}}
			for _, u := range updates {
				if u.ID > r.After && u.Item == r.ID && len(out.Updates) < 200 {
					out.Updates = append(out.Updates, u)
				}
			}
			return out, nil
		}
		return control.Response{}, fmt.Errorf("unexpected %s", r.Op)
	}}
}

func ops(c *recorder) []string {
	var out []string
	for _, r := range c.sent() {
		out = append(out, r.Op)
	}
	return out
}

// gather runs the page's three refresh requests together, as the browser
// sends them, and answers their bodies.
func gather(t *testing.T, d *dashboard, targets ...string) []string {
	t.Helper()
	bodies := make([]string, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, body := d.get(t, "GET", "127.0.0.1:7332", target)
			if res.StatusCode != 200 {
				t.Errorf("GET %s = %d %s", target, res.StatusCode, body)
			}
			bodies[i] = body
		}()
	}
	wg.Wait()
	return bodies
}

// barrier holds each caller until n have arrived, so a test passes only when
// the calls are made together; one that waits for another would time out.
type barrier struct {
	mu      sync.Mutex
	arrived int
	n       int
	open    chan struct{}
}

func newBarrier(n int) *barrier { return &barrier{n: n, open: make(chan struct{})} }

func (b *barrier) wait() error {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.open)
	}
	b.mu.Unlock()
	select {
	case <-b.open:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("only %d of %d calls arrived together", b.arrived, b.n)
	}
}

func TestAPlansRefreshWithACommunicationsPanelMakesThreeParallelCalls(t *testing.T) {
	c := aggregateServer([]control.Update{upd(1, "p24", "ask", "Approve?"), upd(7, "p24", "say", "new")})
	together := newBarrier(3)
	inner := c.reply
	c.reply = func(r control.Request) (control.Response, error) {
		if err := together.wait(); err != nil {
			return control.Response{}, err
		}
		return inner(r)
	}
	d := newDashboard(c)
	// What the page requests on a refresh: the overview, the plan on show
	// and the open thread after its cursor.
	bodies := gather(t, d, "/api/overview", "/api/plans/p24", "/api/items/p24/thread?after=1")
	got := ops(c)
	slices.Sort(got)
	if want := []string{"overview", "plan-detail", "updates"}; !slices.Equal(got, want) {
		t.Fatalf("control calls %v, want %v", got, want)
	}
	var bundle struct {
		Inbox, Sessions, Plans struct{ Data json.RawMessage }
	}
	if err := json.Unmarshal([]byte(bodies[0]), &bundle); err != nil {
		t.Fatal(err)
	}
	for name, part := range map[string]json.RawMessage{"inbox": bundle.Inbox.Data, "sessions": bundle.Sessions.Data, "plans": bundle.Plans.Data} {
		if len(part) == 0 {
			t.Errorf("the overview has no %s: %s", name, bodies[0])
		}
	}
	for _, want := range []string{`Merge it?`, `"feedback":2`, `"id":"p24"`, `"total":4`} {
		if !strings.Contains(bodies[0], want) {
			t.Errorf("overview lacks %s: %s", want, bodies[0])
		}
	}
	if !strings.Contains(bodies[1], `"in_progress":2`) || !strings.Contains(bodies[2], `"id":7`) || !strings.Contains(bodies[2], `"questions":[{"id":1,"state":"open","replies":2}]`) {
		t.Fatalf("plan %s\nthread %s", bodies[1], bodies[2])
	}
}

func TestAThreadRefreshReadsOnlyAfterItsCursor(t *testing.T) {
	var all []control.Update
	for i := 1; i <= 10; i++ {
		all = append(all, upd(i, "t6", "say", "m"))
	}
	c := aggregateServer(all)
	d := newDashboard(c)
	_, body := d.get(t, "GET", "127.0.0.1:7332", "/api/items/t6/thread?after=8")
	reads := c.sent()
	if len(reads) != 1 || reads[0].Op != "updates" || reads[0].After != 8 || reads[0].ID != "t6" {
		t.Fatalf("calls %+v, want one updates read after 8", reads)
	}
	var env struct{ Data Thread }
	if err := json.Unmarshal([]byte(body), &env); err != nil || !reflect.DeepEqual(messageIDs(env.Data), []int{9, 10}) || env.Data.Next != 10 {
		t.Fatalf("thread = %s (%v), want messages 9 and 10 and the cursor at 10", body, err)
	}
}

func TestIncrementalThreadWindowsAndCursor(t *testing.T) {
	var all []control.Update
	for i := 1; i <= 450; i++ {
		kind := "say"
		if i%2 == 0 {
			kind = "state" // not a message: the cursor still moves past it
		}
		all = append(all, upd(i, "t7", kind, "m"))
	}
	for _, tc := range []struct {
		name          string
		after, anchor int
		wantAfter     []int
		count, next   int
		more          bool
	}{
		{"a window of fifty messages", 0, 0, []int{0}, pageSize, 99, true},
		{"nothing left after the end", 450, 0, []int{450}, 0, 450, false},
		{"the end past messages that are not shown", 440, 0, []int{440}, 5, 450, false},
		{"an anchor beyond the first window", 0, 301, []int{0, 200}, 151, 301, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pc := &pagedCaller{updates: all, questions: []control.QuestionStatus{{ID: 3, State: "resolved", Resolution: 9}}}
			up := newUpstream(pc, func() time.Time { return t0 }, nil)
			env, status := up.thread(context.Background(), "t7", tc.after, tc.anchor)
			th, _ := env.Data.(Thread)
			if status != 200 || len(th.Messages) != tc.count || th.Next != tc.next || th.More != tc.more {
				t.Fatalf("thread = %d, %d messages, next %d, more %v; want %d, %d, %v", status, len(th.Messages), th.Next, th.More, tc.count, tc.next, tc.more)
			}
			if want := []Question{{ID: 3, State: "resolved", Resolution: 9}}; !reflect.DeepEqual(th.Questions, want) {
				t.Fatalf("questions = %+v, want the server's statuses %+v", th.Questions, want)
			}
			var got []int
			for _, r := range pc.reads {
				if r.Op == "updates" {
					got = append(got, r.After)
				}
			}
			if !slices.Equal(got, tc.wantAfter) {
				t.Fatalf("update reads after %v, want %v", got, tc.wantAfter)
			}
		})
	}
}

// olderServer answers the aggregate reads with the status an older control
// server gives for an operation it does not know, and every other read from
// the same fixtures as the aggregate server.
func olderServer(status int, message string) *recorder {
	modern := aggregateServer(nil)
	return &recorder{aggregates: true, reply: func(r control.Request) (control.Response, error) {
		switch {
		case aggregateOp(r.Op):
			return control.Response{}, &control.Problem{Status: status, Message: message}
		case r.Op == "list" && r.State == "repo":
			return control.Response{Items: fixtureRepos()}, nil
		case r.Op == "list" && r.State == "plan":
			return control.Response{Items: fixturePlans()}, nil
		case r.Op == "list":
			tasks, _ := inboxItems()
			return control.Response{Items: tasks[:2]}, nil
		case r.Op == "plan-view":
			return control.Response{Items: fixtureView()}, nil
		case r.Op == "attempts":
			return control.Response{Attempts: fixtureAttempts()}, nil
		case r.Op == "session-view":
			return control.Response{Sessions: fixtureSessions()}, nil
		case r.Op == "inbox":
			return control.Response{Updates: []control.Update{inboxUpdate(5, "ask", "t8", "Merge it?", questioning)}}, nil
		}
		if r.Op == "get" {
			return control.Response{Item: &control.Item{ID: r.ID, Kind: "task", Title: "T"}}, nil
		}
		got, err := modern.reply(r)
		got.Questions = nil // statuses came with the aggregate reads
		return got, err
	}}
}

// opCounts counts the control calls by operation.
func opCounts(c *recorder) map[string]int {
	count := map[string]int{}
	for _, op := range ops(c) {
		count[op]++
	}
	return count
}

// aggregateTries counts the times the aggregate reads were asked for.
func aggregateTries(c *recorder) int {
	n := 0
	for op, k := range opCounts(c) {
		if aggregateOp(op) {
			n += k
		}
	}
	return n
}

func TestAnOlderControlServerGetsTodaysCallsAndThePageStillRenders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		message string
	}{
		{"400", 400, `unknown operation "overview"`},
		{"404", 404, "Not Found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := olderServer(tc.status, tc.message)
			d := newDashboard(c)
			bodies := gather(t, d, "/api/overview", "/api/plans/p24")
			for _, want := range []string{`Merge it?`, `"feedback":2`, `"id":"p24"`} {
				if !strings.Contains(bodies[0], want) {
					t.Errorf("overview from an older server lacks %s: %s", want, bodies[0])
				}
			}
			if !strings.Contains(bodies[1], `"in_progress":2`) {
				t.Errorf("plan from an older server = %s", bodies[1])
			}
			count := opCounts(c)
			if count["list"] != 5 || count["plan-view"] != 1 || count["inbox"] != 1 || count["session-view"] < 2 || count["attempts"] != 1 {
				t.Fatalf("calls %v, want today's reads", count)
			}
			if count["overview"] > 1 || count["plan-detail"] > 1 {
				t.Fatalf("calls %v: the aggregate reads were tried again per call", count)
			}
			// Learned once: a second refresh does not ask again.
			before := aggregateTries(c)
			gather(t, d, "/api/overview", "/api/plans/p24", "/api/items/t6/thread?after=0")
			if after := aggregateTries(c); after != before {
				t.Fatalf("aggregate reads went from %d to %d tries: an older server is learned once", before, after)
			}
		})
	}
}

func TestAnOlderServerIsAskedAgainAfterAWhile(t *testing.T) {
	c := olderServer(400, `unknown operation "overview"`)
	d := newDashboard(c)
	tries := func() int {
		n := 0
		for _, op := range ops(c) {
			if op == "overview" {
				n++
			}
		}
		return n
	}
	d.get(t, "GET", "127.0.0.1:7332", "/api/sessions")
	d.get(t, "GET", "127.0.0.1:7332", "/api/sessions")
	if tries() != 1 {
		t.Fatalf("overview tried %d times, want once", tries())
	}
	d.now = d.now.Add(aggregateRetry)
	// The server was upgraded meanwhile.
	modern := aggregateServer(nil)
	c.reply = modern.reply
	d.get(t, "GET", "127.0.0.1:7332", "/api/sessions")
	if tries() != 2 {
		t.Fatalf("overview tried %d times, want one more after %v", tries(), aggregateRetry)
	}
	d.get(t, "GET", "127.0.0.1:7332", "/api/sessions")
	if tries() != 3 {
		t.Fatalf("overview tried %d times, want it kept on once the server answers", tries())
	}
}

func TestAMissingPlanIsNotAnOlderServer(t *testing.T) {
	c := aggregateServer(nil)
	inner := c.reply
	c.reply = func(r control.Request) (control.Response, error) {
		if r.Op == "plan-detail" && r.ID == "p404" {
			return control.Response{}, &control.Problem{Status: 404, Message: "unknown plan"}
		}
		return inner(r)
	}
	d := newDashboard(c)
	if res, _ := d.get(t, "GET", "127.0.0.1:7332", "/api/plans/p404"); res.StatusCode != 404 {
		t.Fatalf("missing plan = %d, want 404", res.StatusCode)
	}
	d.get(t, "GET", "127.0.0.1:7332", "/api/plans/p24")
	if got := ops(c); !slices.Equal(got, []string{"plan-detail", "plan-detail"}) {
		t.Fatalf("calls %v, want plan-detail each time: an unknown plan says nothing about the server", got)
	}
}

func TestUnknownOpIsOnlyA400OrANotFound(t *testing.T) {
	for _, tc := range []struct {
		op   string
		err  error
		want bool
	}{
		{"overview", &control.Problem{Status: 400, Message: "unknown operation"}, true},
		{"overview", &control.Problem{Status: 404, Message: "Not Found"}, true},
		{"plan-detail", &control.Problem{Status: 404, Message: "Not Found"}, true},
		{"plan-detail", &control.Problem{Status: 404, Message: "unknown plan"}, false},
		{"overview", &control.Problem{Status: 403, Message: "this operation requires the operator"}, false},
		{"overview", &control.Problem{Status: 500, Message: "boom"}, false},
		{"overview", context.DeadlineExceeded, false},
	} {
		if got := unknownOp(tc.op, tc.err); got != tc.want {
			t.Errorf("unknownOp(%s, %v) = %v, want %v", tc.op, tc.err, got, tc.want)
		}
	}
}

func TestOverviewKeepsEachViewDuringAnOutage(t *testing.T) {
	c := aggregateServer(nil)
	d := newDashboard(c)
	d.get(t, "GET", "127.0.0.1:7332", "/api/overview")
	c.reply = func(control.Request) (control.Response, error) {
		return control.Response{}, &control.Problem{Status: 500, Message: "db down"}
	}
	_, body := d.get(t, "GET", "127.0.0.1:7332", "/api/overview")
	var b Bundle
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]Envelope{"inbox": b.Inbox, "sessions": b.Sessions, "plans": b.Plans} {
		if !env.Stale || env.Data == nil || !strings.Contains(env.Error, "db down") {
			t.Errorf("%s during an outage = %+v, want the last data marked stale", name, env)
		}
	}
	fresh := newDashboard(c)
	_, body = fresh.get(t, "GET", "127.0.0.1:7332", "/api/overview")
	if err := json.Unmarshal([]byte(body), &b); err != nil || b.Inbox.Data != nil || b.Inbox.Error == "" || b.Plans.Error == "" {
		t.Fatalf("first load during an outage = %s, want an error in each view and no data", body)
	}
}

func TestAFullOverviewInboxFallsBackToItsPages(t *testing.T) {
	c := aggregateServer(nil)
	inner := c.reply
	full := make([]control.Update, inboxPage)
	for i := range full {
		full[i] = inboxUpdate(i+1, "say", "t8", "m", nil)
	}
	c.reply = func(r control.Request) (control.Response, error) {
		switch r.Op {
		case "overview":
			got, _ := inner(r)
			got.Updates = full
			return got, nil
		case "inbox":
			if r.After == 0 {
				return control.Response{Updates: full}, nil
			}
			return control.Response{Updates: []control.Update{inboxUpdate(inboxPage+1, "ask", "t8", "Last", questioning)}}, nil
		case "list":
			tasks, repos := inboxItems()
			if r.State == "repo" {
				return control.Response{Items: repos}, nil
			}
			return control.Response{Items: tasks}, nil
		case "session-view":
			return control.Response{}, nil
		}
		return inner(r)
	}
	_, body := newDashboard(c).get(t, "GET", "127.0.0.1:7332", "/api/inbox")
	var env struct{ Data InboxList }
	if err := json.Unmarshal([]byte(body), &env); err != nil || len(env.Data.Cards) != inboxPage+1 {
		t.Fatalf("inbox = %d cards (%v), want every page of it", len(env.Data.Cards), err)
	}
}

func TestOverviewAndPlanDetailAreDashboardReads(t *testing.T) {
	for _, op := range []string{"overview", "plan-detail"} {
		if !slices.Contains(readOps, op) {
			t.Errorf("%s is not on the dashboard's allowed list", op)
		}
		if slices.Contains(writeOps, op) {
			t.Errorf("%s is a write", op)
		}
	}
	rc := &recorder{aggregates: true}
	up := newUpstream(rc, time.Now, nil)
	if _, err := up.call(context.Background(), control.Request{Op: "overview"}); err != nil {
		t.Fatal(err)
	}
	if res, _ := post(t, newWriter(rc), attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/overview", `{"key":"k"}`); res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST overview = %d, want 405", res.StatusCode)
	}
}
