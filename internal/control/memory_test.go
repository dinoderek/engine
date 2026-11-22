package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// durable fails unless memory holds exactly what a new open would read.
func durable(t *testing.T, s *Store) {
	t.Helper()
	disk, _, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(disk, s.state) {
		t.Fatalf("memory and disk differ:\n%s\n%s", fingerprint(s.state), fingerprint(disk))
	}
}

// counted records the SQL the store sends from now on.
func counted(s *Store) *[]string {
	var sent []string
	s.run = func(ctx context.Context, path, sql string) ([]byte, error) {
		sent = append(sent, sql)
		return SQLite(ctx, path, sql)
	}
	return &sent
}

func TestReadsNeverQueryAndWritesSendOnlyChangedRecords(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "one"})
	sent := counted(s.Store)
	if _, err := s.Store.Snapshot(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Request{{Op: "get", ID: plan.ID}, {Op: "list"}, {Op: "updates", ID: plan.ID}, {Op: "session-view"}, {Op: "inbox"}} {
		call(t, s, op, r)
	}
	if len(*sent) != 0 {
		t.Fatalf("reads ran SQL: %q", *sent)
	}
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "two"})
	if len(*sent) != 1 || strings.Contains((*sent)[0], "SELECT") {
		t.Fatalf("a write sent %q", *sent)
	}
	// The comment is one update and its receipt; no other record is resent.
	if n := strings.Count((*sent)[0], "INSERT INTO records"); n != 2 {
		t.Fatalf("a comment wrote %d records: %s", n, (*sent)[0])
	}
	durable(t, s.Store)
}

// TestReadOperationsChangeNoRecord holds every read operation to its
// promise, on a state with a running task and an open question: a read runs
// on a view, which would drop a change.
func TestReadOperationsChangeNoRecord(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item)
	worker, _ := dispatch(t, s, task)
	call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Which way?", Recipient: "operator", RequiresReply: true})
	requests := map[string]Request{
		"get": {ID: plan.ID}, "list": {}, "plan-view": {ID: plan.ID}, "updates": {ID: task.ID}, "inbox": {},
		"attempts": {ID: task.ID}, "merge-queue": {ID: repo.ID}, "session-view": {}, "host-status": {},
	}
	for _, name := range readOps {
		r, ok := requests[name]
		if !ok {
			t.Fatalf("no request for the read operation %s", name)
		}
		r.Op = name
		for _, a := range []Actor{op, worker} {
			before, _ := s.Store.Snapshot()
			after := before.clone()
			_, err := s.apply(&after, a, r)
			var refused *Problem
			if err != nil && !errors.As(err, &refused) {
				t.Fatal(name, err)
			}
			if changes, _ := diff(&before, &after); len(changes) != 0 {
				t.Fatalf("%s as %s changed %+v", name, a.Role, changes)
			}
		}
	}
}

func TestFailedCommitLeavesMemoryAndDiskUnchanged(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	before, _ := s.Store.Snapshot()
	s.Store.run = func(context.Context, string, string) ([]byte, error) { return nil, errors.New("disk full") }
	if _, err := s.Execute(op, Request{Op: "say", ID: plan.ID, Body: "lost", Key: "k"}); err == nil {
		t.Fatal("a failed commit succeeded")
	}
	refused := errors.New("refused")
	err := s.Store.Transaction(func(st *State) error {
		delete(st.Items, plan.ID)
		st.Updates[0].Body = "rewritten"
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("got %v", err)
	}
	s.Store.run = SQLite
	after, _ := s.Store.Snapshot()
	if fingerprint(before) != fingerprint(after) {
		t.Fatal("a failed transaction changed memory")
	}
	durable(t, s.Store)
}

func TestUnencodableRecordFailsBeforeAnyWrite(t *testing.T) {
	s := service(t)
	sent := counted(s.Store)
	d := &differ{}
	d.put("item/x", func() {})
	if d.err == nil {
		t.Fatal("an unencodable record was accepted")
	}
	if _, err := decode([]change{{key: "item/x", data: "invalid"}}); err == nil {
		t.Fatal("an undecodable record was accepted")
	}
	if len(*sent) != 0 {
		t.Fatalf("wrote %q", *sent)
	}
}

func TestSnapshotMutationsNeverReachTheStore(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	a := newTask(t, s, repo.ID, plan.ID)
	call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Next", Body: "## Outcome\nWorks\n## Tests\nProve it", Depends: []string{a.ID}}})
	before, _ := s.Store.Snapshot()
	want := fingerprint(before)
	sent := counted(s.Store)
	for id, item := range before.Items {
		for i := range item.Depends {
			item.Depends[i] = "changed"
		}
		delete(before.Items, id)
	}
	for i := range before.Updates {
		before.Updates[i].Body = "changed"
		if before.Updates[i].Snapshot != nil {
			before.Updates[i].Snapshot.Title = "changed"
		}
	}
	for _, r := range before.Receipts {
		if r.Response.Item != nil {
			r.Response.Item.Title = "changed"
		}
	}
	before.Next = 99
	after, _ := s.Store.Snapshot()
	if fingerprint(after) != want || len(*sent) != 0 {
		t.Fatal("a snapshot's mutation reached the store")
	}
	durable(t, s.Store)
}

// fill sets every field under v to a non-zero value, with one element in
// each slice and map and a target behind each pointer.
func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		e := reflect.New(v.Type().Elem()).Elem()
		fill(e)
		v.SetMapIndex(reflect.ValueOf("k"), e)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				fill(v.Field(i))
			}
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64:
		v.SetInt(7)
	case reflect.Float64:
		v.SetFloat(7)
	}
}

// shared returns the paths where a and b reach the same memory.
func shared(path string, a, b reflect.Value) []string {
	var out []string
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			return []string{path}
		}
		return shared(path, a.Elem(), b.Elem())
	case reflect.Slice:
		if a.Len() > 0 && a.Pointer() == b.Pointer() {
			return []string{path}
		}
		for i := range a.Len() {
			out = append(out, shared(path+"[]", a.Index(i), b.Index(i))...)
		}
	case reflect.Map:
		if a.Pointer() == b.Pointer() {
			return []string{path}
		}
		for _, k := range a.MapKeys() {
			out = append(out, shared(path+"[]", a.MapIndex(k), b.MapIndex(k))...)
		}
	case reflect.Struct:
		for i := range a.NumField() {
			out = append(out, shared(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))...)
		}
	}
	return out
}

// TestCloneSharesNoMemory fills every field a state can reach, so a field
// added later without its copy fails here.
func TestCloneSharesNoMemory(t *testing.T) {
	var st State
	fill(reflect.ValueOf(&st).Elem())
	if st.Receipts["k"].Response.Fleet.Hosts[0].Windows[0].Name != "x" || *st.Updates[0].Snapshot.Question.Recommended != 7 {
		t.Fatalf("the state was not filled: %+v", st)
	}
	c := st.clone()
	if !reflect.DeepEqual(st, c) {
		t.Fatal("the clone differs")
	}
	if got := shared("State", reflect.ValueOf(st), reflect.ValueOf(c)); len(got) != 0 {
		t.Fatalf("the clone shares %v", got)
	}
	c.Updates[0].Snapshot = st.Updates[0].Snapshot
	c.Receipts["k"].Response.Items[0].Depends = st.Receipts["k"].Response.Items[0].Depends
	if got := shared("State", reflect.ValueOf(st), reflect.ValueOf(c)); len(got) != 2 {
		t.Fatalf("two shared fields were reported as %v", got)
	}
	var empty State
	if c := empty.clone(); c.Updates != nil || c.Receipts == nil {
		t.Fatalf("an empty state cloned to %+v", c)
	}
	withEmpty := State{Receipts: map[string]receipt{"k": {}}}
	if c := withEmpty.clone(); !reflect.DeepEqual(c.Receipts, withEmpty.Receipts) {
		t.Fatal("an empty response changed in the clone")
	}
}

func TestEveryKindOfChangeIsDurable(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "one"})
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "two"})
	steps := map[string]func(*State){
		"replace an update": func(st *State) { st.Updates[1].Body = "edited" },
		"append and replace": func(st *State) {
			st.Updates[0].Left = "x"
			st.update(op, Request{ID: plan.ID}, "say", "three", s.now())
		},
		"delete an update":   func(st *State) { st.Updates = append(st.Updates[:1], st.Updates[2:]...) },
		"reuse an update id": func(st *State) { u := st.Updates[0]; u.Body = "again"; st.Updates = append(st.Updates, u) },
		"truncate history":   func(st *State) { st.Updates = st.Updates[:1] },
		"delete records": func(st *State) {
			delete(st.Items, repo.ID)
			clear(st.Receipts)
			st.Auth["k"] = Actor{Name: "n"}
		},
		"add and drop the rest": func(st *State) {
			delete(st.Auth, "k")
			st.Attempts["a1"] = Attempt{ID: "a1"}
			st.Queue["t1"] = QueueEntry{Task: "t1"}
			st.Next = 40
		},
		"drop them": func(st *State) { delete(st.Attempts, "a1"); delete(st.Queue, "t1") },
	}
	for _, name := range []string{"replace an update", "append and replace", "delete an update", "reuse an update id", "truncate history", "delete records", "add and drop the rest", "drop them"} {
		if err := s.Store.Transaction(func(st *State) error { steps[name](st); return nil }); err != nil {
			t.Fatal(name, err)
		}
		durable(t, s.Store)
	}
	want, _ := s.Store.Snapshot()
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	got, _ := reopened.Snapshot()
	if !reflect.DeepEqual(got, want) || len(got.Updates) != 1 || got.Next != 40 {
		t.Fatalf("a restart read %+v, committed %+v", got, want)
	}
}

func TestUnchangedTransactionWritesNothing(t *testing.T) {
	s := service(t)
	repo, _ := repoPlan(t, s)
	sent := counted(s.Store)
	err := s.Store.Transaction(func(st *State) error {
		// An empty list encodes as a missing one: the same record.
		item := st.Items[repo.ID]
		item.Depends = []string{}
		st.Items[repo.ID] = item
		return nil
	})
	if err != nil || len(*sent) != 0 {
		t.Fatalf("an unchanged transaction wrote %q (%v)", *sent, err)
	}
}

// TestReadersSeeOnlyCommittedStates moves a count between two records in
// each transaction; a reader must never see it part moved.
func TestReadersSeeOnlyCommittedStates(t *testing.T) {
	s := service(t)
	seed := func(st *State) error {
		st.Attempts["a"] = Attempt{ID: "a", Seen: 50}
		st.Attempts["b"] = Attempt{ID: "b"}
		return nil
	}
	if err := s.Store.Transaction(seed); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				st, err := s.Store.Snapshot()
				if sum := st.Attempts["a"].Seen + st.Attempts["b"].Seen; err != nil || sum != 50 {
					t.Errorf("a reader saw a sum of %d (%v)", sum, err)
					return
				}
				// A reader's own changes stay its own.
				st.Attempts["a"] = Attempt{}
				select {
				case <-done:
					return
				default:
				}
			}
		})
	}
	for range 20 {
		err := s.Store.Transaction(func(st *State) error {
			a, b := st.Attempts["a"], st.Attempts["b"]
			a.Seen--
			st.Attempts["a"] = a
			b.Seen++
			st.Attempts["b"] = b
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}
	close(done)
	wg.Wait()
	st, _ := s.Store.Snapshot()
	if st.Attempts["b"].Seen != 20 {
		t.Fatalf("writes were lost: %+v", st.Attempts)
	}
	durable(t, s.Store)
}
