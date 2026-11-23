package control

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestRepoBySlugFindsOnlyARepositoryWithThatSlug(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	err := s.Store.Read(bg, func(tx *Tx) error {
		if got, ok := tx.RepoBySlug("owner/demo"); !ok || got.ID != repo.ID {
			t.Errorf("RepoBySlug(owner/demo) = %q, %v; want %s", got.ID, ok, repo.ID)
		}
		if _, ok := tx.RepoBySlug("owner/other"); ok {
			t.Error("RepoBySlug found a repository that does not exist")
		}
		// A plan carries no slug, and an empty slug names no repository.
		if _, ok := tx.RepoBySlug(plan.Slug); ok {
			t.Error("RepoBySlug matched a plan by its empty slug")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNextIDNeverRepeatsAndSurvivesARolledBackWrite(t *testing.T) {
	s := service(t)
	var first string
	if err := s.Store.Write(bg, func(tx *Tx) error { first = tx.NextID("a"); return errBoom }); !errors.Is(err, errBoom) {
		t.Fatalf("Write returned %v", err)
	}
	var again, other string
	if err := s.Store.Write(bg, func(tx *Tx) error { again, other = tx.NextID("a"), tx.NextID("t"); return nil }); err != nil {
		t.Fatal(err)
	}
	if again != first || Number(other) != Number(again)+1 || again[0] != 'a' || other[0] != 't' {
		t.Fatalf("a rolled-back %s, then %s and %s", first, again, other)
	}
}

var errBoom = errors.New("boom")

func TestAttemptAccessorsSelectByHostStateAndKind(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	err := s.Store.Write(bg, func(tx *Tx) error {
		for _, at := range []Attempt{
			{ID: "a1", Item: task.ID, Host: "devbox", Harness: "claude", State: "running"},
			{ID: "a2", Item: task.ID, Host: "devbox", Harness: "claude", State: "ended"},
			{ID: "a3", Item: task.ID, Host: "mac", Harness: "", State: "running"},
			{ID: "a4", Item: plan.ID, Host: "devbox", Harness: "claude", State: "running"},
		} {
			tx.PutAttempt(at)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	attemptIDs := func(list []Attempt) []string {
		var out []string
		for _, at := range list {
			out = append(out, at.ID)
		}
		slices.Sort(out)
		return out
	}
	err = s.Store.Read(bg, func(tx *Tx) error {
		for name, c := range map[string]struct {
			got  []Attempt
			want []string
		}{
			"on devbox":         {tx.AttemptsOnHost("devbox"), []string{"a1", "a2", "a4"}},
			"on a host no run":  {tx.AttemptsOnHost("nowhere"), nil},
			"unended":           {tx.UnendedAttempts(), []string{"a1", "a3", "a4"}},
			"hosted task, both": {tx.HostedTaskAttempts(), []string{"a1", "a2"}},
		} {
			if got := attemptIDs(c.got); !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueueAccessorsKeepARepositoryQueueInOrder(t *testing.T) {
	s := service(t)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutQueueEntry(QueueEntry{Task: "t3", Repo: "r1", Seq: 30})
		tx.PutQueueEntry(QueueEntry{Task: "t1", Repo: "r1", Seq: 10})
		tx.PutQueueEntry(QueueEntry{Task: "t2", Repo: "r2", Seq: 20})
		tx.PutQueueEntry(QueueEntry{Task: "t4", Repo: "r1", Seq: 20})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	queued := func(repo string) (out []string) {
		_ = s.Store.Read(bg, func(tx *Tx) error {
			for _, e := range tx.QueueOf(repo) {
				out = append(out, e.Task)
			}
			return nil
		})
		return out
	}
	if got := queued("r1"); !slices.Equal(got, []string{"t1", "t4", "t3"}) {
		t.Fatalf("r1 queue %v, want t1 t4 t3", got)
	}
	if err = s.Store.Write(bg, func(tx *Tx) error { tx.DeleteQueueEntry("t4"); return nil }); err != nil {
		t.Fatal(err)
	}
	if got := queued("r1"); !slices.Equal(got, []string{"t1", "t3"}) {
		t.Fatalf("r1 queue after a delete %v, want t1 t3", got)
	}
	if got := queued("r3"); len(got) != 0 {
		t.Fatalf("an unknown repository has queue %v", got)
	}
}

func TestHostPassAndUpgradeRecordsRoundTripThroughTheStore(t *testing.T) {
	s := service(t)
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutCredential("hash", Actor{Name: "a1", Role: "worker", Attempt: "a1"})
		tx.PutHost("devbox", HostRecord{Paused: true, TmuxStart: 77})
		tx.SetPassAt(at)
		tx.SetUpgrade(UpgradeRecord{Start: at, End: at.Add(time.Minute)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Store.Read(bg, func(tx *Tx) error {
		if a, ok := tx.Credential("hash"); !ok || a.Attempt != "a1" {
			t.Errorf("credential %+v, %v", a, ok)
		}
		if rec := tx.Host("devbox"); !rec.Paused || rec.TmuxStart != 77 {
			t.Errorf("host record %+v", rec)
		}
		if rec := tx.Host("unknown"); rec.Paused || rec.TmuxStart != 0 {
			t.Errorf("an unknown host has record %+v", rec)
		}
		if !tx.PassAt().Equal(at) {
			t.Errorf("pass at %v", tx.PassAt())
		}
		if u := tx.Upgrade(); !u.Start.Equal(at) || !u.End.Equal(at.Add(time.Minute)) {
			t.Errorf("upgrade %+v", u)
		}
		// HostRecords is a copy: changing it changes nothing stored.
		records := tx.HostRecords()
		records["devbox"] = HostRecord{}
		delete(records, "devbox")
		if !tx.Host("devbox").Paused {
			t.Error("changing the HostRecords copy changed the stored record")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateQueriesFindAuthorsAndTheNewestOfAKind(t *testing.T) {
	s := service(t)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.AppendUpdate(Actor{Name: "a1"}, Request{ID: "t1"}, "say", "one", at0)
		tx.AppendUpdate(Actor{Name: "a2"}, Request{ID: "t1"}, "merged", "first", at0)
		tx.AppendUpdate(Actor{Name: "a3"}, Request{ID: "t2"}, "merged", "other task", at0)
		tx.AppendUpdate(Actor{Name: "a1"}, Request{ID: "t1"}, "merged", "newest", at0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Store.Read(bg, func(tx *Tx) error {
		if wrote := tx.Authors(); len(wrote) != 3 || !wrote["a1"] || !wrote["a2"] || !wrote["a3"] || wrote["a4"] {
			t.Errorf("authors %v", wrote)
		}
		if u, ok := tx.LatestUpdate("t1", "merged"); !ok || u.Body != "newest" {
			t.Errorf("latest merged of t1 = %+v, %v", u, ok)
		}
		if _, ok := tx.LatestUpdate("t1", "cleaned"); ok {
			t.Error("LatestUpdate found a kind nothing wrote")
		}
		if _, ok := tx.LatestUpdate("t9", "merged"); ok {
			t.Error("LatestUpdate found an update of an item with none")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var at0 = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func TestNewWriteAccessorsRefuseAReadTransaction(t *testing.T) {
	s := service(t)
	writes := map[string]func(*Tx){
		"NextID":           func(tx *Tx) { tx.NextID("a") },
		"PutAttempt":       func(tx *Tx) { tx.PutAttempt(Attempt{ID: "a1"}) },
		"PutCredential":    func(tx *Tx) { tx.PutCredential("h", Actor{}) },
		"PutQueueEntry":    func(tx *Tx) { tx.PutQueueEntry(QueueEntry{Task: "t1"}) },
		"DeleteQueueEntry": func(tx *Tx) { tx.DeleteQueueEntry("t1") },
		"PutHost":          func(tx *Tx) { tx.PutHost("h", HostRecord{}) },
		"SetPassAt":        func(tx *Tx) { tx.SetPassAt(at0) },
		"SetUpgrade":       func(tx *Tx) { tx.SetUpgrade(UpgradeRecord{}) },
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
