package control

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixtureSize is the number of each kind of record in a benchmark database.
type fixtureSize struct{ items, attempts, updates, receipts int }

// liveSessions is how many tasks of a benchmark database run an attempt.
const liveSessions = 4

// fullSize is the size of a long-lived server's database: 500 items, 1,000
// attempts, 20,000 updates carrying item snapshots and 2,000 receipts, about
// 27 MB of records. The race detector slows the store about twenty times, so
// a build with it holds a tenth of that, enough to check every answer.
func fullSize() fixtureSize {
	if raceEnabled {
		return fixtureSize{items: 50, attempts: 100, updates: 2000, receipts: 200}
	}
	return fixtureSize{items: 500, attempts: 1000, updates: 20000, receipts: 2000}
}

// planTasks is how many tasks each plan of a benchmark database holds.
const planTasks = 20

var benchAt = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

// buildFixture writes a closed database of the given size and returns its
// path. Each task's current attempt is its newest; the last liveSessions run.
// Every 200th update asks the operator a question, three in four answered, and
// every 100th tells them something.
func buildFixture(tb testing.TB, z fixtureSize) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "control.db")
	store, err := Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	err = store.Write(context.Background(), func(tx *Tx) error {
		items, tasks := benchItems(z)
		for i := range z.attempts {
			id, task := fmt.Sprintf("a%d", i+600), i%len(tasks)
			if live := i - (z.attempts - liveSessions); live >= 0 {
				task = live
			}
			at := Attempt{ID: id, Item: items[tasks[task]].ID, Host: "devbox", Harness: "claude", RunID: "demo-" + id, State: "ended", Started: benchAt, Ended: benchAt}
			if i >= z.attempts-liveSessions {
				at.State, at.Ended = "running", time.Time{}
				items[tasks[task]].State = "running"
			}
			items[tasks[task]].Attempt = id
			tx.PutAttempt(at)
		}
		for _, item := range items {
			tx.PutItem(item)
		}
		for i := range z.updates {
			tx.PutUpdate(benchUpdate(items, i))
			if i%(z.updates/z.receipts) == 0 {
				item := items[i%len(items)]
				tx.PutReceipt(fmt.Sprintf("%064d", i), receipt{Digest: "d", Response: Response{Item: &item, Updates: []Update{benchUpdate(items, i)}}}, benchAt)
			}
		}
		tx.exec("UPDATE counters SET value=? WHERE name='next'", z.updates+2000)
		return nil
	})
	if err == nil {
		err = store.Close()
	}
	if err != nil {
		tb.Fatal(err)
	}
	return path
}

// benchItems are the items of a database of size z: a repository, then
// plans each followed by its tasks, and the indexes of the tasks. The first
// plan is p2.
func benchItems(z fixtureSize) ([]Item, []int) {
	items := make([]Item, z.items)
	var tasks []int
	plan := ""
	for i := range items {
		kind := "task"
		switch {
		case i == 0:
			kind = "repo"
		case (i-1)%(planTasks+1) == 0:
			kind = "plan"
		}
		id := fmt.Sprintf("%s%d", kind[:1], i+1)
		items[i] = Item{ID: id, Kind: kind, Version: 3, Repo: "r1", Plan: plan, Title: "A task", Body: strings.Repeat("requirement ", 80), State: "done", Slug: "owner/demo"}
		switch kind {
		case "repo":
			items[i].Repo, items[i].Plan = "", ""
		case "plan":
			items[i].Plan, plan = "", id
		default:
			tasks = append(tasks, i)
		}
	}
	return items, tasks
}

// benchUpdate is update i+1 of the fixture.
func benchUpdate(items []Item, i int) Update {
	item := items[i%len(items)]
	u := Update{ID: i + 1, Item: item.ID, Author: "operator", At: benchAt, Kind: "say", Body: strings.Repeat("progress ", 20), Snapshot: slim(item)}
	switch {
	case i%200 == 0:
		u.Author, u.Kind, u.Recipient, u.RequiresReply = "a600", "ask", "operator", true
	case i%200 == 1 && (i/200)%4 != 0:
		u.Kind, u.ReplyTo = "resolved", i
	case i%100 == 0:
		u.Author, u.Recipient = "coordinator", "operator"
	}
	return u
}

// benchStore opens a database of the given size, which no other store owns.
func benchStore(tb testing.TB, z fixtureSize) Service {
	tb.Helper()
	store, err := Open(buildFixture(tb, z))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = store.Close() })
	return Service{Store: store, Now: func() time.Time { return benchAt }}
}

// operation is one measured request of the store.
type operation struct {
	name string
	// target is what the store should answer in; the gate allows headroom
	// times it.
	target time.Duration
	run    func() (Response, error)
	// check is what a correct answer satisfies.
	check func(Response) error
}

// headroom is how many times over its target an operation may run before the
// gate fails: far enough that a loaded machine never trips it, near enough
// that work over the whole history does.
const headroom = 10

const (
	pointTarget = time.Millisecond
	scanTarget  = 5 * time.Millisecond
)

// operations are the requests whose targets the performance gate holds the
// store to, on a database of size z.
func operations(s Service, z fixtureSize) []operation {
	read := func(r Request) func() (Response, error) {
		return func() (Response, error) { return s.Execute(op, r) }
	}
	need := func(ok func(Response) bool) func(Response) error {
		return func(r Response) error {
			if !ok(r) {
				return fmt.Errorf("unexpected answer %+v", r)
			}
			return nil
		}
	}
	return []operation{
		{"get", pointTarget, read(Request{Op: "get", ID: "t3"}), need(func(r Response) bool { return r.Item != nil && r.Item.ID == "t3" })},
		{"updates", pointTarget, read(Request{Op: "updates", ID: "t3", After: z.updates - 1000}), need(func(r Response) bool {
			return len(r.Updates) > 0 && r.Updates[0].Item == "t3"
		})},
		{"session-view", pointTarget, read(Request{Op: "session-view"}), need(func(r Response) bool { return len(r.Sessions) == liveSessions })},
		{"inbox", scanTarget, read(Request{Op: "inbox"}), need(func(r Response) bool { return len(r.Updates) > 0 })},
		{"overview", scanTarget, read(Request{Op: "overview"}), need(func(r Response) bool {
			return len(r.Updates) > 0 && len(r.Sessions) == liveSessions && len(r.Items) >= 2
		})},
		{"plan-detail", scanTarget, read(Request{Op: "plan-detail", ID: "p2"}), need(func(r Response) bool {
			return len(r.Items) > planTasks && len(r.Sessions) == liveSessions
		})},
		{"write say", scanTarget, func() (Response, error) {
			return s.Execute(op, Request{Op: "say", ID: "t3", Body: "noted", Key: Secret()})
		}, need(func(r Response) bool { return len(r.Updates) == 1 })},
		{"write one record", scanTarget, func() (Response, error) {
			return Response{}, s.Store.Write(context.Background(), func(tx *Tx) error {
				at := tx.attempt("a600")
				at.Seen++
				tx.PutAttempt(at)
				return nil
			})
		}, nil},
	}
}

// samples is how many times the gate times an operation; it keeps the
// fastest, which a loaded machine's pauses cannot raise, while work over the
// whole history raises every run.
const samples = 11

// measure is the fastest time of run over samples runs after one warm-up,
// read from now, which returns the time elapsed since a fixed start.
func measure(run func() (Response, error), now func() time.Duration) (time.Duration, Response, error) {
	r, err := run()
	took := make([]time.Duration, samples)
	for i := 0; i < samples && err == nil; i++ {
		start := now()
		if r, err = run(); err == nil {
			took[i] = now() - start
		}
	}
	slices.Sort(took)
	return took[0], r, err
}

// gate runs every operation and returns what is wrong with it: a wrong answer
// always, and, when timed, a fastest run over headroom times its target. An
// untimed run answers each operation once.
func gate(ops []operation, factor float64, timed bool, now func() time.Duration) []string {
	var failures []string
	for _, o := range ops {
		var took time.Duration
		var r Response
		var err error
		if timed {
			took, r, err = measure(o.run, now)
		} else {
			r, err = o.run()
		}
		if err == nil && o.check != nil {
			err = o.check(r)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", o.name, err))
		} else if timed && float64(took) > factor*float64(o.target) {
			failures = append(failures, fmt.Sprintf("%s took %v, over %gx its %v target", o.name, took, factor, o.target))
		}
	}
	return failures
}

// since is a clock of the real time.
func since() func() time.Duration {
	start := time.Now()
	return func() time.Duration { return time.Since(start) }
}

// openTarget is what opening a database should take, whatever its history.
const openTarget = 200 * time.Millisecond

// opening is the operation of opening, then closing, the database at path.
func opening(path string) operation {
	return operation{"open", openTarget, func() (Response, error) {
		store, err := Open(path)
		if err != nil {
			return Response{}, err
		}
		return Response{}, store.Close()
	}, nil}
}

// timed reports whether this build can time the store: the race detector
// slows it past what the targets mean.
func timed() bool { return !raceEnabled }

func TestStoreAnswersWithinItsPerformanceTargets(t *testing.T) {
	z := fullSize()
	path := buildFixture(t, z)
	if failures := gate([]operation{opening(path)}, headroom, timed(), since()); len(failures) > 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
	store := openAt(t, path)
	s := Service{Store: store, Now: func() time.Time { return benchAt }}
	if failures := gate(operations(s, z), headroom, timed(), since()); len(failures) > 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
}

// addHistory appends n updates about t3 to the closed database at path,
// numbered from first.
func addHistory(t *testing.T, path string, first, n int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(), `WITH RECURSIVE n(i) AS (SELECT ?1 UNION ALL SELECT i+1 FROM n WHERE i<?1+?2-1)
INSERT INTO updates(id,item,kind,author,data) SELECT i,'t3','say','operator',
 json_object('id',i,'item','t3','author','operator','at','2026-11-07T09:00:00Z','kind','say','body','progress progress progress') FROM n`, first, n)
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpeningWithTenTimesTheHistoryStaysWithinTheBound(t *testing.T) {
	z := fullSize()
	path := buildFixture(t, z)
	for _, history := range []int{z.updates, 10 * z.updates} {
		if failures := gate([]operation{opening(path)}, headroom, timed(), since()); len(failures) > 0 {
			t.Fatalf("with %d updates: %s", history, strings.Join(failures, "\n"))
		}
		if history == z.updates {
			addHistory(t, path, history+1, 9*z.updates)
		}
	}
	var n int
	err := openAt(t, path).Read(context.Background(), func(tx *Tx) error {
		n = len(slices.Collect(tx.UpdatesAfter(10*z.updates-1, "")))
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("the added history is missing: %d, %v", n, err)
	}
}

// stepping is a clock that advances by step at every read.
func stepping(step time.Duration) func() time.Duration {
	var t time.Duration
	return func() time.Duration { t += step; return t }
}

func TestGateFailsAnOperationOverTenTimesItsTargetAndNamesIt(t *testing.T) {
	fine := func() (Response, error) { return Response{}, nil }
	ops := []operation{{"get", pointTarget, fine, nil}, {"inbox", scanTarget, fine, nil}}
	// Each run reads as 20 ms: twice get's allowance, a third of inbox's.
	failures := gate(ops, headroom, true, stepping(20*time.Millisecond))
	if len(failures) != 1 || !strings.HasPrefix(failures[0], "get took 20ms, over 10x its 1ms target") {
		t.Fatalf("failures = %q", failures)
	}
	// An allowance exactly met passes.
	if failures = gate(ops[:1], headroom, true, stepping(10*time.Millisecond)); len(failures) != 0 {
		t.Fatalf("failed at the bound: %q", failures)
	}
}

func TestGateReportsAWrongAnswerWithoutTimingIt(t *testing.T) {
	wrong := func(Response) error { return fmt.Errorf("no sessions") }
	fine := func() (Response, error) { return Response{}, nil }
	broken := func() (Response, error) { return Response{}, fmt.Errorf("closed") }
	ops := []operation{{"session-view", pointTarget, fine, wrong}, {"get", pointTarget, broken, nil}, {"inbox", scanTarget, fine, nil}}
	failures := gate(ops, headroom, false, nil)
	want := []string{"session-view: no sessions", "get: closed"}
	if !slices.Equal(failures, want) {
		t.Fatalf("failures = %q, want %q", failures, want)
	}
}

func TestMeasureKeepsTheFastestOfItsRuns(t *testing.T) {
	var reads int
	now := func() time.Duration { reads++; return time.Duration(reads*reads) * time.Millisecond }
	got, _, err := measure(func() (Response, error) { return Response{}, nil }, now)
	// Run i is timed by reads 2i+1 and 2i+2: (2i+2)^2-(2i+1)^2 = 4i+3 ms.
	if err != nil || got != 3*time.Millisecond {
		t.Fatalf("fastest = %v, %v", got, err)
	}
}

// BenchmarkStore measures each gated operation against a full-size database.
func BenchmarkStore(b *testing.B) {
	z := fullSize()
	for _, o := range operations(benchStore(b, z), z) {
		b.Run(o.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := o.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
