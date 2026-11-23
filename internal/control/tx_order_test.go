package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func itemIDs(items []Item) []string {
	var out []string
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func attemptIDs(ats []Attempt) []string {
	var out []string
	for _, at := range ats {
		out = append(out, at.ID)
	}
	return out
}

func TestItemAndAttemptListingsFollowTheNumberInTheirIDs(t *testing.T) {
	tx := blank(t)
	for _, id := range []string{"t10", "t9", "p2", "t100", "r1"} {
		tx.PutItem(Item{ID: id, Kind: map[byte]string{'t': "task", 'p': "plan", 'r': "repo"}[id[0]], Repo: "r1", Plan: "p2", Coordinating: true, Attempt: "a" + id[1:]})
		tx.PutAttempt(Attempt{ID: "a" + id[1:], Item: id, Host: "devbox", Harness: "claude", State: "running"})
	}
	for name, got := range map[string][]string{
		"ItemsByKindUnder":  itemIDs(tx.ItemsByKindUnder("", "")),
		"ItemsOfPlan":       itemIDs(tx.ItemsOfPlan("p2")),
		"CoordinatedItems":  itemIDs(tx.CoordinatedItems()),
		"tasks under r1":    itemIDs(tx.ItemsByKindUnder("task", "r1")),
		"AttemptsFor":       attemptIDs(tx.AttemptsFor("")),
		"CurrentAttempts":   attemptIDs(tx.CurrentAttempts()),
		"UnendedAttempts":   attemptIDs(tx.UnendedAttempts()),
		"AttemptsOnHost":    attemptIDs(tx.AttemptsOnHost("devbox")),
		"HostedTaskAttempt": attemptIDs(tx.HostedTaskAttemptsOf("devbox", "claude")),
	} {
		want := map[string][]string{
			"ItemsByKindUnder": {"r1", "p2", "t9", "t10", "t100"}, "ItemsOfPlan": {"r1", "p2", "t9", "t10", "t100"},
			"CoordinatedItems": {"r1", "p2", "t9", "t10", "t100"}, "tasks under r1": {"t9", "t10", "t100"},
			"HostedTaskAttempt": {"a9", "a10", "a100"},
		}[name]
		if want == nil {
			want = []string{"a1", "a2", "a9", "a10", "a100"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

func TestQueueListingsBreakSequenceTiesByTask(t *testing.T) {
	tx := blank(t)
	for _, e := range []QueueEntry{{Task: "t9", Repo: "r2", Seq: 5}, {Task: "t8", Repo: "r1", Seq: 7}, {Task: "t7", Repo: "r1", Seq: 7}, {Task: "t6", Repo: "r1", Seq: 3}} {
		tx.PutQueueEntry(e)
	}
	var got []string
	for _, e := range tx.QueueOf("r1") {
		got = append(got, e.Task)
	}
	if !slices.Equal(got, []string{"t6", "t7", "t8"}) {
		t.Errorf("QueueOf: %v", got)
	}
	got = nil
	for _, e := range tx.QueueEntries() {
		got = append(got, e.Task)
	}
	if !slices.Equal(got, []string{"t6", "t7", "t8", "t9"}) {
		t.Errorf("QueueEntries: %v", got)
	}
}

func TestUpdateIteratorsWalkEveryPageInOrderAndStopEarly(t *testing.T) {
	tx := blank(t)
	n := 2*pageSize + 3
	for i := 1; i <= n; i++ {
		u := Update{ID: i, Item: "t1", Kind: "say", RequiresReply: i%2 == 0}
		if i%3 == 0 {
			u.Item, u.Task = "p2", "t1"
		}
		if i%5 == 0 {
			u.Item, u.Task = "t9", ""
		}
		tx.PutUpdate(u)
	}
	walk := func(seq func(func(Update) bool)) []int {
		var out []int
		for u := range seq {
			out = append(out, u.ID)
		}
		return out
	}
	all := walk(tx.UpdatesAfter(0, ""))
	if len(all) != n || !slices.IsSorted(all) || all[0] != 1 {
		t.Fatalf("UpdatesAfter: %d updates, sorted %v", len(all), slices.IsSorted(all))
	}
	about := walk(tx.UpdatesAboutNewestFirst("t1"))
	if len(about) == 0 || !slices.IsSortedFunc(about, func(a, b int) int { return b - a }) || slices.ContainsFunc(about, func(id int) bool { return id%5 == 0 }) {
		t.Fatalf("UpdatesAboutNewestFirst: %v", about)
	}
	if want := n - n/5; len(about) != want {
		t.Fatalf("UpdatesAboutNewestFirst: %d updates, want %d", len(about), want)
	}
	questions := walk(tx.QuestionsAbout("t1"))
	for _, id := range questions {
		if id%2 != 0 || id%5 == 0 {
			t.Fatalf("QuestionsAbout yielded %d", id)
		}
	}
	if !slices.IsSorted(questions) || len(walk(tx.UpdatesAfter(pageSize+1, "t9"))) != len(walk(tx.UpdatesAfter(0, "t9")))-pageSize/5 {
		t.Fatal("an item's updates after a cursor")
	}
	seen := 0
	for range tx.UpdatesAfter(0, "") {
		if seen++; seen == pageSize+1 {
			break
		}
	}
	if seen != pageSize+1 {
		t.Fatalf("stopped after %d", seen)
	}
}

func TestAFailedQueryFailsItsTransaction(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	if _, err := s.Store.writer.Exec("UPDATE items SET data='not json' WHERE id=?", plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(op, Request{Op: "get", ID: plan.ID}); err == nil || errors.As(err, new(*Problem)) {
		t.Fatalf("a read of a corrupt row: %v; want a storage error", err)
	}
	wrote := false
	err := s.Store.Write(context.Background(), func(tx *Tx) error {
		tx.AppendUpdate(op, Request{}, "say", "before the failure", time.Time{})
		_, ok := tx.Item(plan.ID)
		wrote = !ok
		tx.PutItem(Item{ID: "r9", Kind: "repo"})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") || !wrote {
		t.Fatalf("a write that read a corrupt row: %v", err)
	}
	out, err := s.Store.sql("SELECT count(*) AS n FROM updates WHERE kind='say'; SELECT count(*) AS n FROM items WHERE id='r9'")
	if err != nil || string(out) != `[{"n":0}][{"n":0}]` {
		t.Fatalf("a failed write committed: %s %v", out, err)
	}
}

func TestDispatchTokensAreTheCredentialsOfAnAttemptsReceipts(t *testing.T) {
	tx := blank(t)
	at := &Attempt{ID: "a2"}
	tx.PutReceipt("k1", receipt{Response: Response{Attempt: at, Credential: "first"}}, time.Time{})
	tx.PutReceipt("k2", receipt{Response: Response{Attempt: at}}, time.Time{})
	tx.PutReceipt("k3", receipt{Response: Response{Attempt: &Attempt{ID: "a3"}, Credential: "other"}}, time.Time{})
	tx.PutReceipt("k4", receipt{Response: Response{Attempt: at, Credential: "second"}}, time.Time{})
	if got := tx.DispatchTokens("a2"); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("DispatchTokens: %v", got)
	}
	if got := tx.DispatchTokens("a9"); len(got) != 0 {
		t.Fatalf("DispatchTokens of an attempt with none: %v", got)
	}
}

func TestOnlyAWriteIsBoundedInTime(t *testing.T) {
	s := service(t)
	var readBound, writeBound bool
	var writeLeft time.Duration
	if err := s.Store.Read(context.Background(), func(tx *Tx) error {
		_, readBound = tx.ctx.Deadline()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Write(context.Background(), func(tx *Tx) error {
		var at time.Time
		at, writeBound = tx.ctx.Deadline()
		writeLeft = time.Until(at)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if readBound || !writeBound || writeLeft > writeTimeout {
		t.Fatalf("read bounded %v; write bounded %v with %v left, want at most %v", readBound, writeBound, writeLeft, writeTimeout)
	}
	s.Store.writeTimeout = time.Nanosecond
	if err := s.Store.Write(context.Background(), func(*Tx) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a write past its bound: %v", err)
	}
	if err := s.Store.Read(context.Background(), func(tx *Tx) error { tx.Item("r1"); return nil }); err != nil {
		t.Fatalf("a read beside a short write bound: %v", err)
	}
}

func TestARecordThatCannotBeEncodedFailsItsWrite(t *testing.T) {
	s := service(t)
	err := s.Store.Write(context.Background(), func(tx *Tx) error {
		tx.PutAttempt(Attempt{ID: "a1", State: "running", Started: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "control record") {
		t.Fatalf("a write of an unencodable record: %v", err)
	}
	if got := mustSnapshot(t, s).Attempts; len(got) != 0 {
		t.Fatalf("it stored %+v", got)
	}
}

func TestAnItemsDependenciesAndCapabilitiesFollowItsLatestPutAndItsDelete(t *testing.T) {
	tx := blank(t)
	rows := func() string {
		var out []string
		tx.rows(func(r *sql.Rows) error {
			var item, value string
			err := r.Scan(&item, &value)
			out = append(out, item+":"+value)
			return err
		}, "SELECT item, depends FROM item_deps UNION ALL SELECT item, 'cap ' || cap FROM item_requires ORDER BY 1, 2")
		return strings.Join(out, " ")
	}
	tx.PutItem(Item{ID: "t2", Kind: "task", Depends: []string{"t9", "t8"}, Requires: []string{"linux", "gpu"}})
	tx.PutItem(Item{ID: "t3", Kind: "task", Depends: []string{"t2"}, Requires: []string{"gpu"}})
	tx.PutItem(Item{ID: "t2", Kind: "task", Depends: []string{"t8"}, Requires: []string{"gpu"}})
	if got, want := rows(), "t2:cap gpu t2:t8 t3:cap gpu t3:t2"; got != want {
		t.Fatalf("after a replace: %q, want %q", got, want)
	}
	tx.DeleteItem("t3")
	if got, want := rows(), "t2:cap gpu t2:t8"; got != want || tx.err != nil {
		t.Fatalf("after a delete: %q, want %q (%v)", got, want, tx.err)
	}
}

// TestReadersSeeOnlyCommittedStates moves a count between two attempts in
// each write; a reader beside them must never see it part moved.
func TestReadersSeeOnlyCommittedStates(t *testing.T) {
	s := service(t)
	edit(t, s.Store, func(tx *Tx) {
		tx.PutAttempt(Attempt{ID: "a1", Seen: 50})
		tx.PutAttempt(Attempt{ID: "a2"})
	})
	var wg sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				err := s.Store.Read(context.Background(), func(tx *Tx) error {
					if sum := tx.attempt("a1").Seen + tx.attempt("a2").Seen; sum != 50 {
						return fmt.Errorf("a reader saw a sum of %d", sum)
					}
					return nil
				})
				if err != nil {
					t.Error(err)
					return
				}
				select {
				case <-done:
					return
				default:
				}
			}
		})
	}
	for range 20 {
		edit(t, s.Store, func(tx *Tx) {
			a, b := tx.attempt("a1"), tx.attempt("a2")
			a.Seen--
			b.Seen++
			tx.PutAttempt(a)
			tx.PutAttempt(b)
		})
	}
	close(done)
	wg.Wait()
	if got := mustSnapshot(t, s).Attempts["a2"].Seen; got != 20 {
		t.Fatalf("writes were lost: a2 saw %d", got)
	}
}

func TestAResolutionIsTheOldestOfAQuestionsResolvingReplies(t *testing.T) {
	tx := blank(t)
	for _, u := range []Update{{ID: 1, Kind: "ask", RequiresReply: true}, {ID: 2, Kind: "acknowledged", ReplyTo: 1},
		{ID: 3, Kind: KindPaneAnswer, ReplyTo: 1}, {ID: 4, Kind: "resolved", ReplyTo: 1}, {ID: 5, Kind: "resolved", ReplyTo: 9}} {
		tx.PutUpdate(u)
	}
	if got := tx.Resolution(1); got != 3 {
		t.Fatalf("Resolution(1) = %d, want the oldest resolving reply, 3", got)
	}
	if got := tx.Resolution(2); got != 0 {
		t.Fatalf("Resolution of an unresolved update = %d", got)
	}
}

func TestAReceiptIsCreatedAtTheServicesClock(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "stamped", Key: "k"})
	out, err := s.Store.sql("SELECT DISTINCT created FROM receipts")
	if want := `[{"created":"2026-11-07T09:00:00.000Z"}]`; err != nil || string(out) != want {
		t.Fatalf("receipts created %s (%v), want every one at the service's clock: %s", out, err, want)
	}
}
