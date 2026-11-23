package control

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchStore opens a database the size of a long-lived server's: 500 items,
// 1,000 attempts, 20,000 updates carrying item snapshots and 2,000 receipts,
// about 27 MB of records.
func benchStore(b *testing.B) Service {
	b.Helper()
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	path := filepath.Join(b.TempDir(), "control.db")
	empty, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	err = empty.Write(context.Background(), func(tx *Tx) error {
		items := make([]Item, 500)
		for i := range items {
			kind, plan := "task", "p2"
			if i < 2 {
				kind, plan = []string{"repo", "plan"}[i], ""
			}
			items[i] = Item{ID: fmt.Sprintf("%s%d", kind[:1], i+1), Kind: kind, Version: 3, Repo: "r1", Plan: plan, Title: "A task", Body: strings.Repeat("requirement ", 80), State: "done", Slug: "owner/demo"}
			tx.PutItem(items[i])
		}
		for i := range 1000 {
			id := fmt.Sprintf("a%d", i+600)
			tx.PutAttempt(Attempt{ID: id, Item: items[2+i%498].ID, Host: "devbox", Harness: "claude", RunID: "demo-" + id, State: "ended", Started: at, Ended: at})
		}
		for i := range 20000 {
			item := items[i%len(items)]
			u := Update{ID: i + 1, Item: item.ID, Author: "operator", At: at, Kind: "say", Body: strings.Repeat("progress ", 20), Snapshot: slim(item)}
			tx.PutUpdate(u)
			if i%10 == 0 {
				tx.PutReceipt(fmt.Sprintf("%064d", i), receipt{Digest: "d", Response: Response{Item: &item, Updates: []Update{u}}}, at)
			}
		}
		tx.exec("UPDATE counters SET value=2000 WHERE name='next'")
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	if err = empty.Close(); err != nil {
		b.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	return Service{Store: store, Now: func() time.Time { return at }}
}

// BenchmarkStore measures requests against a full-size database.
func BenchmarkStore(b *testing.B) {
	s := benchStore(b)
	reads := map[string]Request{
		"get":          {Op: "get", ID: "t3"},
		"list":         {Op: "list"},
		"updates":      {Op: "updates", ID: "t3", After: 19000},
		"session-view": {Op: "session-view"},
	}
	for name, r := range reads {
		b.Run("read/"+name, func(b *testing.B) {
			for b.Loop() {
				if _, err := s.Execute(op, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("write/one-record", func(b *testing.B) {
		n := 0
		for b.Loop() {
			n++
			err := s.Store.Write(context.Background(), func(tx *Tx) error {
				at := tx.attempt("a600")
				at.Seen = n
				tx.PutAttempt(at)
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("write/comment", func(b *testing.B) {
		for b.Loop() {
			if _, err := s.Execute(op, Request{Op: "say", ID: "t3", Body: "noted", Key: Secret()}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
