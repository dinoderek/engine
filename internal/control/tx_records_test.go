package control

import (
	"errors"
	"fmt"
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
			"hosted task, both": {tx.HostedTaskAttemptsOf("devbox", "claude"), []string{"a1", "a2"}},
			"other harness":     {tx.HostedTaskAttemptsOf("devbox", "codex"), nil},
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
		for author, want := range map[string]bool{"a1": true, "a2": true, "a3": true, "a4": false, "": false} {
			if got := tx.Wrote(author); got != want {
				t.Errorf("Wrote(%q) = %v, want %v", author, got, want)
			}
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
		"DeleteItem":       func(tx *Tx) { tx.DeleteItem("t1") },
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

func TestItemAccessorsFindCoordinatedItemsAndDeleteAnItem(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	err := s.Store.Write(bg, func(tx *Tx) error {
		coordinated := tx.item(plan.ID)
		coordinated.Coordinating = true
		tx.PutItem(coordinated)
		quiet := tx.item(task.ID)
		quiet.Coordinating = false
		tx.PutItem(quiet)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Store.Read(bg, func(tx *Tx) error {
		if got := tx.CoordinatedItems(); len(got) != 1 || got[0].ID != plan.ID {
			t.Errorf("CoordinatedItems = %v, want only the plan", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Write(bg, func(tx *Tx) error { tx.DeleteItem(task.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	_ = s.Store.Read(bg, func(tx *Tx) error {
		if _, ok := tx.Item(task.ID); ok {
			t.Error("the deleted item is still there")
		}
		return nil
	})
}

func TestQueueEntriesAreEveryRepositorysQueue(t *testing.T) {
	s := service(t)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutQueueEntry(QueueEntry{Task: "t1", Repo: "r1"})
		tx.PutQueueEntry(QueueEntry{Task: "t2", Repo: "r2"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Store.Read(bg, func(tx *Tx) error {
		var tasks []string
		for _, e := range tx.QueueEntries() {
			tasks = append(tasks, e.Task)
		}
		slices.Sort(tasks)
		if !slices.Equal(tasks, []string{"t1", "t2"}) {
			t.Errorf("QueueEntries = %v, want t1 and t2", tasks)
		}
		return nil
	})
}

func TestLimitedPairsAreWhereAHostedTaskAttemptEndedAtALimit(t *testing.T) {
	s := service(t)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutItem(Item{ID: "t1", Kind: "task"})
		tx.PutItem(Item{ID: "p2", Kind: "plan"})
		for _, at := range []Attempt{
			{ID: "a1", Item: "t1", Host: "devbox", Harness: "claude", State: "ended", Limited: true},
			{ID: "a2", Item: "t1", Host: "devbox", Harness: "claude", State: "ended", Limited: true},
			{ID: "a3", Item: "t1", Host: "mac", Harness: "codex", State: "ended"},
			{ID: "a4", Item: "t1", Host: "mac", Harness: "claude", State: "running", Limited: true},
			{ID: "a5", Item: "p2", Host: "box", Harness: "claude", State: "ended", Limited: true},
			{ID: "a6", Item: "t1", Host: "", Harness: "claude", State: "ended", Limited: true},
			{ID: "a7", Item: "t1", Host: "box", Harness: "", State: "ended", Limited: true},
			{ID: "a8", Item: "t1", Host: "box", Harness: "codex", State: "ended", Limited: true},
		} {
			tx.PutAttempt(at)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Store.Read(bg, func(tx *Tx) error {
		if got := tx.LimitedPairs(); fmt.Sprint(got) != "[[box codex] [devbox claude]]" {
			t.Errorf("LimitedPairs = %v", got)
		}
		return nil
	})
}

func TestActiveAttemptsAreTheCurrentOnesNotEnded(t *testing.T) {
	s := service(t)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutItem(Item{ID: "t1", Kind: "task", Attempt: "a3"})
		tx.PutItem(Item{ID: "t2", Kind: "task", Attempt: "a4"})
		tx.PutItem(Item{ID: "t3", Kind: "task", Attempt: "a5"})
		tx.PutItem(Item{ID: "t4", Kind: "task"})
		for _, at := range []Attempt{
			{ID: "a1", Item: "t1", State: "running"}, // an older attempt of t1, not its current one
			{ID: "a3", Item: "t1", State: "running"},
			{ID: "a4", Item: "t2", State: "ended"},
			{ID: "a5", Item: "t3", State: "dispatching"},
			{ID: "a10", Item: "t4", State: "running"}, // its item names no attempt
		} {
			tx.PutAttempt(at)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Store.Read(bg, func(tx *Tx) error {
		var ids []string
		for _, at := range tx.ActiveAttempts() {
			ids = append(ids, at.ID)
		}
		if !slices.Equal(ids, []string{"a3", "a5"}) {
			t.Errorf("ActiveAttempts = %v, want a3 and a5", ids)
		}
		return nil
	})
}

func TestQueuedReposAreTheRepositoriesWithEntriesByNumber(t *testing.T) {
	s := service(t)
	err := s.Store.Write(bg, func(tx *Tx) error {
		tx.PutQueueEntry(QueueEntry{Task: "t1", Repo: "r10"})
		tx.PutQueueEntry(QueueEntry{Task: "t2", Repo: "r2"})
		tx.PutQueueEntry(QueueEntry{Task: "t3", Repo: "r2"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Store.Read(bg, func(tx *Tx) error {
		if got := tx.QueuedRepos(); !slices.Equal(got, []string{"r2", "r10"}) {
			t.Errorf("QueuedRepos = %v, want r2 then r10", got)
		}
		return nil
	})
	_ = s.Store.Write(bg, func(tx *Tx) error {
		tx.DeleteQueueEntry("t1")
		tx.DeleteQueueEntry("t2")
		tx.DeleteQueueEntry("t3")
		if got := tx.QueuedRepos(); len(got) != 0 {
			t.Errorf("QueuedRepos of an empty queue = %v", got)
		}
		return nil
	})
}
