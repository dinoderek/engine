package control

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// explaining runs every query on db, first recording its query plan.
type explaining struct {
	t     *testing.T
	db    *sql.DB
	plans []string
}

func (e *explaining) explain(ctx context.Context, query string, args ...any) {
	rows, err := e.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			e.t.Fatal(err)
		}
		e.plans = append(e.plans, detail)
	}
}

func (e *explaining) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return e.db.ExecContext(ctx, query, args...)
}

func (e *explaining) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	e.explain(ctx, query, args...)
	return e.db.QueryContext(ctx, query, args...)
}

func (e *explaining) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	e.explain(ctx, query, args...)
	return e.db.QueryRowContext(ctx, query, args...)
}

// TestEveryScopedAccessorIsAnIndexedQuery fails when an accessor that
// selects by a key, a state or a reply scans a table, or a correlated
// subquery does, as when a query cannot use the index its rows need.
func TestEveryScopedAccessorIsAnIndexedQuery(t *testing.T) {
	store := openAt(t, fixtureDB(t))
	e := &explaining{t: t, db: store.readers}
	tx := &Tx{ctx: context.Background(), q: e}
	walk := func(seq func(func(Update) bool)) {
		for range seq {
		}
	}
	// uses are the indexes an accessor must search: without one, SQLite may
	// still search, walking the whole table by its key.
	uses := map[string][]string{
		"CoordinatedItems": {"items_coordinating"}, "ItemsOfPlan": {"items_plan"}, "RepoBySlug": {"items_kind"},
		"AttemptsFor": {"attempts_item"}, "UnendedAttempts": {"attempts_state"}, "AttemptsOnHost": {"attempts_host"},
		"DispatchTokens": {"receipts_attempt"}, "QueueOf": {"queue_repo"},
		"Wrote": {"updates_author"}, "LimitedPairs": {"attempts_limited"}, "HostedTaskAttemptsOf": {"attempts_host"},
		"ActiveAttempts": {"attempts_state"}, "QueuedRepos": {"queue_repo"},
		"UpdatesAfter": {"updates_item"}, "InboxAfter": {"updates_recipient", "updates_reply"},
		"NoticesBefore": {"updates_recipient", "updates_reply"}, "UpdatesAboutNewestFirst": {"updates_item", "updates_task"},
		"QuestionsAbout": {"updates_item", "updates_task"}, "Resolution": {"updates_reply"}, "LatestUpdate": {"updates_item"},
		"TaskStatesOfPlans": {"items_plan"}, "OwnQuestions": {"updates_asked_item", "updates_asked_task", "updates_reply"},
		"LatestMessages": {"updates_item"},
	}
	for name, read := range map[string]func(){
		"Item":             func() { tx.Item("t3") },
		"CoordinatedItems": func() { tx.CoordinatedItems() },
		"ItemsOfPlan":      func() { tx.ItemsOfPlan("p2") },
		"ItemsByKindUnder": func() {
			tx.ItemsByKindUnder("task", "p2")
			tx.ItemsByKindUnder("", "r1")
			tx.ItemsByKindUnder("task", "")
		},
		"RepoBySlug":              func() { tx.RepoBySlug("owner/demo") },
		"Attempt":                 func() { tx.Attempt("a4") },
		"AttemptsFor":             func() { tx.AttemptsFor("t3") },
		"UnendedAttempts":         func() { tx.UnendedAttempts() },
		"AttemptsOnHost":          func() { tx.AttemptsOnHost("devbox") },
		"HostedTaskAttemptsOf":    func() { tx.HostedTaskAttemptsOf("devbox", "claude") },
		"LimitedPairs":            func() { tx.LimitedPairs() },
		"ActiveAttempts":          func() { tx.ActiveAttempts() },
		"QueuedRepos":             func() { tx.QueuedRepos() },
		"Wrote":                   func() { tx.Wrote("a4") },
		"Credential":              func() { tx.Credential("x") },
		"Receipt":                 func() { tx.Receipt("x") },
		"DispatchTokens":          func() { tx.DispatchTokens("a4") },
		"QueueEntry":              func() { tx.QueueEntry("t5") },
		"QueueOf":                 func() { tx.QueueOf("r1") },
		"Update":                  func() { tx.Update(3) },
		"UpdatesAfter":            func() { walk(tx.UpdatesAfter(10, "")); walk(tx.UpdatesAfter(0, "t3")) },
		"InboxAfter":              func() { walk(tx.InboxAfter(0, "")); walk(tx.InboxAfter(0, "t3")) },
		"NoticesBefore":           func() { walk(tx.NoticesBefore(0)) },
		"UpdatesAboutNewestFirst": func() { walk(tx.UpdatesAboutNewestFirst("t3")) },
		"QuestionsAbout":          func() { walk(tx.QuestionsAbout("t3")) },
		"Resolution":              func() { tx.Resolution(3) },
		"LatestUpdate":            func() { tx.LatestUpdate("t3", "say") },
		"Host":                    func() { tx.Host("devbox") },
		"ItemsByID":               func() { tx.ItemsByID([]string{"t3", "p2"}) },
		"UpdatesByID":             func() { tx.UpdatesByID([]int{3, 4}) },
		"TaskStatesOfPlans":       func() { tx.TaskStatesOfPlans([]string{"p2"}) },
		"OwnQuestions":            func() { tx.OwnQuestions([]string{"t3", "p2"}) },
		"LatestMessages":          func() { tx.LatestMessages([]string{"t3"}, humanKinds) },
	} {
		e.plans = nil
		read()
		if tx.err != nil {
			t.Fatalf("%s: %v", name, tx.err)
		}
		for _, step := range e.plans {
			if strings.HasPrefix(step, "SCAN ") && !scansOnlyWhatIsBounded(step) {
				t.Errorf("%s scans: %v", name, e.plans)
			}
		}
		for _, index := range uses[name] {
			if !strings.Contains(strings.Join(e.plans, " ")+" ", "INDEX "+index+" ") {
				t.Errorf("%s does not search %s: %v", name, index, e.plans)
			}
		}
	}
}

// scansOnlyWhatIsBounded reports whether a scan step walks an index that
// holds only a bounded part of its table: the attempts that hit a usage limit,
// or the merges in flight; or a list of IDs the caller passed, which a
// batched read unpacks with json_each, and the rows of such a query it reads once.
func scansOnlyWhatIsBounded(step string) bool {
	for _, ok := range []string{"SCAN CONSTANT ROW", "USING INDEX attempts_limited", "USING COVERING INDEX queue_repo", " VIRTUAL TABLE INDEX ", "SCAN (subquery-"} {
		if strings.Contains(step, ok) {
			return true
		}
	}
	return false
}
