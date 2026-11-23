package control

import (
	"context"
	"database/sql"
	"errors"
	"iter"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

var bg = context.Background()

func TestReadSeesAConsistentSnapshotWhileWritesRun(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	const writes = 40
	var wg sync.WaitGroup
	wg.Go(func() {
		for range writes {
			err := s.Store.Write(bg, func(tx *Tx) error {
				item, _ := tx.Item(plan.ID)
				item.Version++
				tx.PutItem(item)
				tx.AppendUpdate(op, Request{ID: plan.ID}, "say", "tick", time.Time{})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}
	})
	for range writes {
		// A write puts the item and its update in together, so a snapshot
		// that holds one holds the other.
		err := s.Store.Read(bg, func(tx *Tx) error {
			item, _ := tx.Item(plan.ID)
			ticks := 0
			for u := range tx.UpdatesAfter(0, plan.ID) {
				if u.Kind == "say" {
					ticks++
				}
			}
			if item.Version-plan.Version != ticks {
				t.Errorf("snapshot holds version %d and %d ticks", item.Version, ticks)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func TestAWriteErrorLeavesRecordsUnchangedInMemoryAndOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := Service{Store: store, Now: func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }}
	_, plan := repoPlan(t, s)
	before, _ := store.snapshot()
	boom := errors.New("refused")
	err = store.Write(bg, func(tx *Tx) error {
		item, _ := tx.Item(plan.ID)
		item.Title = "changed"
		tx.PutItem(item)
		tx.AppendUpdate(op, Request{ID: plan.ID}, "say", "lost", time.Time{})
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write returned %v, want the function's error", err)
	}
	check := func(where string, st records) {
		t.Helper()
		if st.Items[plan.ID].Title != plan.Title || len(st.Updates) != len(before.Updates) {
			t.Errorf("%s: title %q with %d updates after a failed write; want %q with %d",
				where, st.Items[plan.ID].Title, len(st.Updates), plan.Title, len(before.Updates))
		}
	}
	now, _ := store.snapshot()
	check("memory", now)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	disk, _ := reopened.snapshot()
	check("disk", disk)
}

func TestAClosedStoreRefusesReadAndWrite(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	ran := false
	fn := func(*Tx) error { ran = true; return nil }
	if err = store.Read(bg, fn); err == nil {
		t.Error("Read on a closed store succeeded")
	}
	if err = store.Write(bg, fn); err == nil {
		t.Error("Write on a closed store succeeded")
	}
	if ran {
		t.Error("a closed store ran the function")
	}
}

func TestACancelledContextRefusesReadAndWrite(t *testing.T) {
	s := service(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	fn := func(*Tx) error { t.Error("ran"); return nil }
	if err := s.Store.Read(ctx, fn); !errors.Is(err, context.Canceled) {
		t.Errorf("Read returned %v, want a cancellation", err)
	}
	if err := s.Store.Write(ctx, fn); !errors.Is(err, context.Canceled) {
		t.Errorf("Write returned %v, want a cancellation", err)
	}
}

func TestAReadTransactionRefusesAWrite(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	writes := map[string]func(*Tx){
		"PutItem":      func(tx *Tx) { tx.PutItem(plan) },
		"PutReceipt":   func(tx *Tx) { tx.PutReceipt("k", receipt{}) },
		"AppendUpdate": func(tx *Tx) { tx.AppendUpdate(op, Request{}, "say", "x", time.Time{}) },
		"PutUpdate":    func(tx *Tx) { tx.PutUpdate(Update{ID: 1}) },
	}
	for name, write := range writes {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s in a read transaction did not panic", name)
				}
			}()
			_ = s.Store.Read(bg, func(tx *Tx) error { write(tx); return nil })
		}()
	}
}

// thread is a plan with a thread of messages to the operator, some answered.
type thread struct {
	plan                                         Item
	openAsk, resolvedAsk, ackedAsk, plain, acked Update
}

func newThread(t *testing.T, s Service) thread {
	t.Helper()
	_, plan := repoPlan(t, s)
	write := func(fn func(*Tx) Update) Update {
		var u Update
		if err := s.Store.Write(bg, func(tx *Tx) error { u = fn(tx); return nil }); err != nil {
			t.Fatal(err)
		}
		return u
	}
	say := func(body string, ask bool) Update {
		return write(func(tx *Tx) Update {
			return tx.AppendUpdate(op, Request{ID: plan.ID, Recipient: "operator", RequiresReply: ask}, "say", body, time.Time{})
		})
	}
	answer := func(kind string, to Update) {
		write(func(tx *Tx) Update {
			return tx.AppendUpdate(op, Request{ID: plan.ID, ReplyTo: to.ID}, kind, "x", time.Time{})
		})
	}
	th := thread{plan: plan, openAsk: say("open", true), resolvedAsk: say("resolved", true), ackedAsk: say("acked ask", true),
		plain: say("plain", false), acked: say("acked", false)}
	answer("resolved", th.resolvedAsk)
	answer("acknowledged", th.ackedAsk)
	answer("acknowledged", th.acked)
	return th
}

func ids(seq iter.Seq[Update]) []int {
	var out []int
	for u := range seq {
		out = append(out, u.ID)
	}
	return out
}

func TestInboxAndNoticeQueriesFollowTheirAnswers(t *testing.T) {
	s := service(t)
	th := newThread(t, s)
	err := s.Store.Read(bg, func(tx *Tx) error {
		if got, want := ids(tx.InboxAfter(0, "")), []int{th.openAsk.ID, th.ackedAsk.ID, th.plain.ID}; !slices.Equal(got, want) {
			t.Errorf("inbox %v, want %v", got, want)
		}
		if got, want := ids(tx.InboxAfter(th.openAsk.ID, "")), []int{th.ackedAsk.ID, th.plain.ID}; !slices.Equal(got, want) {
			t.Errorf("inbox after %d: %v, want %v", th.openAsk.ID, got, want)
		}
		if got := ids(tx.InboxAfter(0, "t999")); len(got) != 0 {
			t.Errorf("inbox about another item: %v", got)
		}
		if got, want := ids(tx.NoticesBefore(0)), []int{th.plain.ID}; !slices.Equal(got, want) {
			t.Errorf("notices %v, want %v", got, want)
		}
		if got := ids(tx.NoticesBefore(th.plain.ID)); len(got) != 0 {
			t.Errorf("notices before %d: %v, want none", th.plain.ID, got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQuestionQueriesFollowTheirResolutions(t *testing.T) {
	s := service(t)
	th := newThread(t, s)
	err := s.Store.Read(bg, func(tx *Tx) error {
		if got, want := ids(tx.QuestionsAbout(th.plan.ID)), []int{th.openAsk.ID, th.resolvedAsk.ID, th.ackedAsk.ID}; !slices.Equal(got, want) {
			t.Errorf("questions %v, want %v", got, want)
		}
		if got := OwnQuestion(tx, th.plan.ID); got != th.openAsk.ID {
			t.Errorf("own question %d, want %d", got, th.openAsk.ID)
		}
		if tx.Resolution(th.resolvedAsk.ID) == 0 || tx.Resolution(th.openAsk.ID) != 0 {
			t.Error("Resolution does not tell the resolved question from the open one")
		}
		newest := ids(tx.UpdatesAboutNewestFirst(th.plan.ID))
		if len(newest) == 0 || !slices.IsSortedFunc(newest, func(a, b int) int { return b - a }) {
			t.Errorf("updates about the plan not newest first: %v", newest)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAFailedCommitLeavesTheRecordsUnchanged(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	before := mustSnapshot(t, s)
	s.Store.commit = func(*sql.Tx) error { return errors.New("disk full") }
	if _, err := s.Execute(op, Request{Op: "say", ID: plan.ID, Body: "lost", Key: "k"}); err == nil {
		t.Fatal("a failed commit succeeded")
	}
	s.Store.commit = (*sql.Tx).Commit
	if after := mustSnapshot(t, s); fingerprint(after) != fingerprint(before) {
		t.Fatal("a failed commit changed the records")
	}
	if out := call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "lost", Key: "k"}); out.Updates[0].Body != "lost" {
		t.Fatalf("the retry: %+v", out)
	}
}

// A commit SQLite took whose success was lost is answered from its receipt
// when the caller retries with the same key: the command applies once.
func TestACommitReportedFailedAfterItTookIsAnsweredFromItsReceipt(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	s.Store.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("reply lost")
	}
	if _, err := s.Execute(op, Request{Op: "say", ID: plan.ID, Body: "once", Key: "k"}); err == nil {
		t.Fatal("the lost reply was reported a success")
	}
	s.Store.commit = (*sql.Tx).Commit
	before := len(mustSnapshot(t, s).Updates)
	again := call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "once", Key: "k"})
	if after := len(mustSnapshot(t, s).Updates); after != before || again.Updates[0].Body != "once" {
		t.Fatalf("the retry applied again: %d updates, then %d; %+v", before, after, again)
	}
}
