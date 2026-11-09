package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestMigrationRetiresLocksAndQueuesPendingRequests builds a version 1 and
// a version 2 database, each with a merge lock held by one task, an
// unanswered merge request from another and a plan in a derived state, and
// reopens it.
func TestMigrationRetiresLocksAndQueuesPendingRequests(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint("from version ", version), func(t *testing.T) { migrateFrom(t, version) })
	}
}

func migrateFrom(t *testing.T, version int) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	holder, a, holding := inReview(t, s, repo, plan)
	plan = *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	waiter, b, _ := inReview(t, s, repo, plan)
	idle := newTask(t, s, repo.ID, plan.ID)
	older := call(t, s, b, ready(waiter.ID, good())).Updates[0]
	call(t, s, a, ready(holder.ID, good()))
	// Version 1 had no queue: the requests were bare updates, and a lock row.
	lock, _ := json.Marshal(map[string]any{"repo": repo.ID, "attempt": holding.Attempt.ID, "token": "secret", "acquired": "2026-11-07T09:00:00Z"})
	sql := "BEGIN IMMEDIATE; DELETE FROM records WHERE key LIKE 'queue/%'; INSERT INTO records(key,data) VALUES(" +
		literal("lock/"+repo.ID) + "," + literal(string(lock)) + "); UPDATE records SET data = json_set(data, '$.state', 'executing') WHERE key = " +
		literal("item/"+plan.ID) + fmt.Sprintf("; PRAGMA user_version=%d; COMMIT;", version)
	if _, err := s.Store.sql(sql); err != nil {
		t.Fatal(err)
	}
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	st, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if st.Items[plan.ID].State != "planned" {
		t.Fatalf("plan state %s", st.Items[plan.ID].State)
	}
	queue := Queue(&st, repo.ID)
	if len(queue) != 2 || queue[0].Task != waiter.ID || queue[0].Seq != older.ID || queue[1].Task != holder.ID || queue[0].Phase != QueueWaiting {
		t.Fatalf("queue %+v", queue)
	}
	told := map[string]bool{}
	for _, u := range st.Updates {
		if u.Kind == LockRetired && strings.Contains(u.Body, "merge-ready") {
			told[u.Item] = true
		}
	}
	if !told[holder.ID] || !told[waiter.ID] || told[idle.ID] || len(told) != 2 {
		t.Fatalf("lock-retired notices on %v", told)
	}
	out, err := store.sql("SELECT count(*) AS n FROM records WHERE key LIKE 'lock/%'; PRAGMA user_version;")
	if err != nil || !strings.Contains(string(out), `"n":0`) || !strings.Contains(string(out), fmt.Sprintf(`"user_version":%d`, schemaVersion)) {
		t.Fatalf("after migration: %s %v", out, err)
	}
}

func TestMigrationTellsTheLockHolderEvenWhenIdle(t *testing.T) {
	st := emptyState()
	st.Items["t1"] = Item{ID: "t1", Kind: "task", State: "todo", Attempt: "a2"}
	st.Attempts["a2"] = Attempt{ID: "a2", Item: "t1", State: "ended"}
	retire(&st, []legacyLock{{Repo: "r1", Attempt: "a2"}}, time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC))
	if len(st.Updates) != 1 || st.Updates[0].Item != "t1" || st.Updates[0].Kind != LockRetired {
		t.Fatalf("updates %+v", st.Updates)
	}
}

func TestMigrationQueuesOnlyUnfinishedTasks(t *testing.T) {
	st := emptyState()
	st.Items["t1"] = Item{ID: "t1", Kind: "task", State: "failed", Attempt: "a2"}
	st.Attempts["a2"] = Attempt{ID: "a2", Item: "t1", State: "ended"}
	st.Updates = []Update{{ID: 1, Item: "t1", Author: "a2", Kind: "merge-request", Merge: &MergeRequest{PR: "p", Head: head}}}
	retire(&st, nil, time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC))
	if len(st.Queue) != 0 {
		t.Fatalf("a failed task was queued: %+v", st.Queue)
	}
}
