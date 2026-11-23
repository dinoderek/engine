package control

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const day = 24 * time.Hour

// retentionClock is a Service whose clock a test moves.
func retentionClock(t *testing.T) (Service, *time.Time) {
	t.Helper()
	s := service(t)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	return s, &now
}

func hasReceipt(t *testing.T, s Service, key string) bool {
	t.Helper()
	var found bool
	if err := s.Store.Read(bg, func(tx *Tx) error { _, found = tx.Receipt(key); return nil }); err != nil {
		t.Fatal(err)
	}
	return found
}

func receiptCount(t *testing.T, s Service) int {
	t.Helper()
	out, err := s.Store.sql("SELECT count(*) AS n FROM receipts")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err = fmt.Sscanf(string(out), `[{"n":%d}]`, &n); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	return n
}

// say posts a message under the fingerprint-visible request key and returns
// that key's receipt key.
func receiptKey(key string) string { return fingerprint([]string{op.Name, op.Attempt, key}) }

func TestAReceiptIsDeletedSevenDaysAfterItWasWritten(t *testing.T) {
	s, now := retentionClock(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "old", Key: "old"})
	*now = now.Add(6*day + 23*time.Hour)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "six days on", Key: "recent"})
	if !hasReceipt(t, s, receiptKey("old")) {
		t.Fatal("a receipt under seven days old was deleted")
	}
	*now = now.Add(day)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "eight days on", Key: "newest"})
	if hasReceipt(t, s, receiptKey("old")) {
		t.Fatal("a receipt over seven days old survived a write")
	}
	if !hasReceipt(t, s, receiptKey("recent")) || !hasReceipt(t, s, receiptKey("newest")) {
		t.Fatal("a receipt under seven days old was deleted")
	}
}

func TestARetryWithinSevenDaysReturnsTheStoredResponse(t *testing.T) {
	s, now := retentionClock(t)
	_, plan := repoPlan(t, s)
	first := call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "once", Key: "k"})
	*now = now.Add(6 * day)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "other", Key: "other"})
	again := call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "once", Key: "k"})
	if len(again.Updates) != 1 || again.Updates[0].ID != first.Updates[0].ID {
		t.Fatalf("the retry wrote again: %+v, first %+v", again.Updates, first.Updates)
	}
}

func TestAKeyReusedAfterItsReceiptIsDeletedIsANewRequest(t *testing.T) {
	s, now := retentionClock(t)
	_, plan := repoPlan(t, s)
	first := call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "once", Key: "k"})
	*now = now.Add(8 * day)
	// The same key with another command is not a conflict, and the same
	// command writes again.
	other := call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "another", Key: "k"})
	if other.Updates[0].ID == first.Updates[0].ID || other.Updates[0].Body != "another" {
		t.Fatalf("the reused key replayed: %+v", other.Updates)
	}
}

func TestADispatchReceiptStaysUntilItsAttemptEnds(t *testing.T) {
	s, now := retentionClock(t)
	at := Attempt{ID: "a7", Item: "t3", State: "running", Host: "devbox"}
	if err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutAttempt(at)
		tx.PutReceipt("dispatch", receipt{Digest: "d", Response: Response{Attempt: &at, Credential: "token"}}, *now)
		tx.PutReceipt("plain", receipt{Digest: "d"}, *now)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, plan := repoPlan(t, s)
	*now = now.Add(8 * day)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "sweep", Key: "k1"})
	if !hasReceipt(t, s, "dispatch") || hasReceipt(t, s, "plain") {
		t.Fatalf("after eight days: dispatch kept %v, plain kept %v", hasReceipt(t, s, "dispatch"), hasReceipt(t, s, "plain"))
	}
	at.State = "ended"
	if err := s.Store.Write(bg, func(tx *Tx) error { tx.PutAttempt(at); return nil }); err != nil {
		t.Fatal(err)
	}
	if !hasReceipt(t, s, "dispatch") {
		t.Fatal("ending the attempt deleted its receipt outside a sweep")
	}
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "sweep again", Key: "k2"})
	if hasReceipt(t, s, "dispatch") {
		t.Fatal("the receipt of an ended attempt survived the sweep")
	}
}

func TestASweepDeletesAtMostTheLimitOldestFirst(t *testing.T) {
	s, now := retentionClock(t)
	if err := s.Store.Write(bg, func(tx *Tx) error {
		for i := range 5 {
			tx.PutReceipt(fmt.Sprintf("k%d", i), receipt{Digest: "d"}, now.Add(time.Duration(i)*time.Minute))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var deleted int
	if err := s.Store.Write(bg, func(tx *Tx) error {
		deleted = tx.DeleteExpiredReceipts(now.Add(day), 3)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if deleted != 3 || hasReceipt(t, s, "k2") || !hasReceipt(t, s, "k3") || !hasReceipt(t, s, "k4") || receiptCount(t, s) != 2 {
		t.Fatalf("deleted %d, %d left; want the 3 oldest gone", deleted, receiptCount(t, s))
	}
}

func TestOnlyAWriteSweepsReceipts(t *testing.T) {
	s, now := retentionClock(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "old", Key: "old"})
	*now = now.Add(8 * day)
	call(t, s, op, Request{Op: "get", ID: plan.ID})
	if !hasReceipt(t, s, receiptKey("old")) {
		t.Fatal("a read deleted a receipt")
	}
}

func TestTheSweepReadsTheCreatedIndexNotEveryReceipt(t *testing.T) {
	s := service(t)
	rows, err := s.Store.writer.Query("EXPLAIN QUERY PLAN "+deleteExpiredReceipts, "2026-01-01T00:00:00.000Z", 100)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "receipts_created") {
		t.Fatalf("the sweep does not search receipts_created:\n%s", joined)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN ") && !strings.Contains(step, "INDEX receipts_created") {
			t.Errorf("the sweep scans: %s", step)
		}
	}
}
