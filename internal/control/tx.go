package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Tx is the typed access to the control records inside one Store.Read or
// Store.Write. Domain code reaches records only through its methods: each
// that selects by a key, state or reply is one indexed query. A Tx is valid only inside the function it is given to.
//
// A failed query leaves the Tx failed: every later accessor returns the zero
// value, and the Read or Write returns the first error, a Write rolling back.
type Tx struct {
	ctx   context.Context
	q     querier
	write bool
	err   error
}

// querier is what a Tx runs its queries on: a database transaction, or a
// database for a test's unscoped reads.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// writeTimeout bounds a write, which holds the one writer.
const writeTimeout = 15 * time.Second

// Read runs fn in a WAL read transaction on the reader pool, and commits
// nothing. Reads run beside each other and beside the writer, and see every
// earlier commit whole or not at all. A read is bounded only by ctx: a
// coordinator pass applies its actions, host calls included, inside one.
func (s *Store) Read(ctx context.Context, fn func(*Tx) error) error {
	return s.run(ctx, false, fn)
}

// Write runs fn in BEGIN IMMEDIATE on the one writer connection: every
// change it makes commits together, and an error from fn rolls it back.
// Writes queue for that connection in Go, never on SQLITE_BUSY.
func (s *Store) Write(ctx context.Context, fn func(*Tx) error) error {
	return s.run(ctx, true, fn)
}

func (s *Store) run(ctx context.Context, write bool, fn func(*Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	closed, db := s.lock == nil, s.readers
	if write {
		db = s.writer
	}
	s.mu.RUnlock()
	// A Close from here on fails the transaction: the lock is never held
	// across fn, which may start another transaction.
	if closed {
		return errors.New("store is closed")
	}
	if write {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.writeTimeout)
		defer cancel()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := &Tx{ctx: ctx, q: tx, write: write}
	if !write {
		// A WAL read transaction takes its snapshot at its first read: take
		// it now, so fn sees the records as they were when Read began.
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT value FROM counters WHERE name='next'").Scan(&n); err != nil {
			return err
		}
	}
	err = fn(t)
	if t.err != nil {
		return t.err
	}
	if err != nil {
		return err
	}
	if !write {
		return tx.Commit()
	}
	return s.commit(tx)
}

// Err is the first query error in this transaction, or nil. A read with
// effects outside the records checks it before each one: after a failure the
// accessors return zero values, so a decision on them may be wrong.
func (t *Tx) Err() error { return t.err }

func (t *Tx) mustWrite() {
	if !t.write {
		panic("control: write in a read transaction")
	}
}

func (t *Tx) fail(err error) {
	if err != nil && t.err == nil {
		t.err = err
	}
}

func (t *Tx) exec(query string, args ...any) {
	if t.err != nil {
		return
	}
	_, err := t.q.ExecContext(t.ctx, query, args...)
	t.fail(err)
}

// encode is v as a data column.
func (t *Tx) encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.fail(fmt.Errorf("control record: %w", err))
	}
	return string(b)
}

// fetch is the record in the data column of the row query finds, if any.
func fetch[T any](t *Tx, query string, args ...any) (T, bool) {
	var v T
	if t.err != nil {
		return v, false
	}
	var data string
	err := t.q.QueryRowContext(t.ctx, query, args...).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false
	}
	if err == nil {
		err = json.Unmarshal([]byte(data), &v)
	}
	t.fail(err)
	return v, err == nil
}

// fetchAll are the records in the data column of every row query finds, in its
// order. The rows are closed before it returns, so a caller can query again
// while it walks them.
func fetchAll[T any](t *Tx, query string, args ...any) []T {
	var out []T
	t.rows(func(rows *sql.Rows) error {
		var data string
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var v T
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	}, query, args...)
	return out
}

// rows runs scan on each row query finds.
func (t *Tx) rows(scan func(*sql.Rows) error, query string, args ...any) {
	if t.err != nil {
		return
	}
	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		t.fail(err)
		return
	}
	for rows.Next() && err == nil {
		err = scan(rows)
	}
	t.fail(errors.Join(err, rows.Err(), rows.Close()))
}

// byNumber orders items and attempts by the number in their IDs, which
// follows creation.
var byNumber = byNumberOf("id")

// byNumberOf orders rows by the number in the ID that column holds.
func byNumberOf(column string) string {
	return " ORDER BY CAST(substr(" + column + ", 2) AS INTEGER), " + column
}

// Item is the item with this ID.
func (t *Tx) Item(id string) (Item, bool) {
	return fetch[Item](t, "SELECT data FROM items WHERE id=?", id)
}

// item is the item with this ID, the zero Item when there is none.
func (t *Tx) item(id string) Item {
	item, _ := t.Item(id)
	return item
}

// attempt is the attempt with this ID, the zero Attempt when there is none.
func (t *Tx) attempt(id string) Attempt {
	at, _ := t.Attempt(id)
	return at
}

// PutItem stores item under its ID.
func (t *Tx) PutItem(item Item) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO items(id,kind,repo,plan,state,coordinating,attempt,slug,version,data) VALUES(?,?,?,?,?,?,?,?,?,?)",
		item.ID, item.Kind, item.Repo, item.Plan, item.State, item.Coordinating, item.Attempt, item.Slug, item.Version, t.encode(item))
	t.exec("DELETE FROM item_deps WHERE item=?", item.ID)
	for _, d := range item.Depends {
		t.exec("INSERT OR IGNORE INTO item_deps(item,depends) VALUES(?,?)", item.ID, d)
	}
	t.exec("DELETE FROM item_requires WHERE item=?", item.ID)
	for _, c := range item.Requires {
		t.exec("INSERT OR IGNORE INTO item_requires(item,cap) VALUES(?,?)", item.ID, c)
	}
}

// DeleteItem removes the item with this ID.
func (t *Tx) DeleteItem(id string) {
	t.mustWrite()
	t.exec("DELETE FROM items WHERE id=?", id)
	t.exec("DELETE FROM item_deps WHERE item=?", id)
	t.exec("DELETE FROM item_requires WHERE item=?", id)
}

// CoordinatedItems are the items whose coordination intent is set, by ID.
func (t *Tx) CoordinatedItems() []Item {
	return fetchAll[Item](t, "SELECT data FROM items WHERE coordinating=1"+byNumber)
}

// ItemsOfPlan are the tasks of plan, by ID.
func (t *Tx) ItemsOfPlan(plan string) []Item {
	return fetchAll[Item](t, "SELECT data FROM items WHERE plan=?"+byNumber, plan)
}

// ItemsByKindUnder are the items of a kind that belong to repo or plan
// parent, by ID. An empty kind or parent matches every one.
func (t *Tx) ItemsByKindUnder(kind, parent string) []Item {
	switch {
	case parent != "" && kind != "":
		return fetchAll[Item](t, "SELECT data FROM items WHERE kind=?1 AND (repo=?2 OR plan=?2)"+byNumber, kind, parent)
	case parent != "":
		return fetchAll[Item](t, "SELECT data FROM items WHERE repo=?1 OR plan=?1"+byNumber, parent)
	case kind != "":
		return fetchAll[Item](t, "SELECT data FROM items WHERE kind=?"+byNumber, kind)
	}
	return fetchAll[Item](t, "SELECT data FROM items"+byNumber)
}

// RepoBySlug is the repository item with this owner/name slug.
func (t *Tx) RepoBySlug(slug string) (Item, bool) {
	return fetch[Item](t, "SELECT data FROM items WHERE kind='repo' AND slug=?"+byNumber+" LIMIT 1", slug)
}

// NextID is the next unused ID of an item or attempt, with prefix.
func (t *Tx) NextID(prefix string) string {
	t.mustWrite()
	var n int
	if t.err == nil {
		t.fail(t.q.QueryRowContext(t.ctx, "UPDATE counters SET value=value+1 WHERE name='next' RETURNING value-1").Scan(&n))
	}
	return fmt.Sprintf("%s%d", prefix, n)
}

// Attempt is the attempt with this ID.
func (t *Tx) Attempt(id string) (Attempt, bool) {
	return fetch[Attempt](t, "SELECT data FROM attempts WHERE id=?", id)
}

// PutAttempt stores at under its ID.
func (t *Tx) PutAttempt(at Attempt) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO attempts(id,item,state,host,data) VALUES(?,?,?,?,?)", at.ID, at.Item, at.State, at.Host, t.encode(at))
}

// AttemptsFor are the attempts of item, by ID. An empty item matches every
// attempt.
func (t *Tx) AttemptsFor(item string) []Attempt {
	if item == "" {
		return fetchAll[Attempt](t, "SELECT data FROM attempts"+byNumber)
	}
	return fetchAll[Attempt](t, "SELECT data FROM attempts WHERE item=?"+byNumber, item)
}

// CurrentAttempts are the attempts that are their item's current one, by
// ID, ended or not.
func (t *Tx) CurrentAttempts() []Attempt {
	return fetchAll[Attempt](t, "SELECT a.data FROM attempts a JOIN items i ON i.attempt=a.id AND i.id=a.item"+byNumberOf("a.id"))
}

// CurrentAttemptsOfPlan are the attempts that are the current one of the plan
// or of one of its tasks, by ID, ended or not.
func (t *Tx) CurrentAttemptsOfPlan(plan string) []Attempt {
	return fetchAll[Attempt](t, "SELECT a.data FROM attempts a JOIN items i ON i.attempt=a.id AND i.id=a.item"+
		" WHERE i.plan=?1 OR i.id=?1"+byNumberOf("a.id"), plan)
}

// UnendedAttempts are the attempts not yet ended, by ID.
func (t *Tx) UnendedAttempts() []Attempt {
	// Two ranges, not !=, so the state index answers it.
	return fetchAll[Attempt](t, "SELECT data FROM attempts WHERE state<'ended' OR state>'ended'"+byNumber)
}

// AttemptsOnHost are the attempts that ran or run on host, by ID.
func (t *Tx) AttemptsOnHost(host string) []Attempt {
	return fetchAll[Attempt](t, "SELECT data FROM attempts WHERE host=?"+byNumber, host)
}

// HostedTaskAttempts are the attempts of tasks that name both their host and
// their harness, by ID.
func (t *Tx) HostedTaskAttempts() []Attempt {
	return fetchAll[Attempt](t, "SELECT a.data FROM attempts a JOIN items i ON i.id=a.item AND i.kind='task'"+
		" WHERE a.host!='' AND json_extract(a.data, '$.harness')!=''"+byNumberOf("a.id"))
}

// Credential is the actor an execution credential's digest names.
func (t *Tx) Credential(hash string) (Actor, bool) {
	return fetch[Actor](t, "SELECT data FROM credentials WHERE hash=?", hash)
}

// PutCredential stores the actor an execution credential's digest names.
func (t *Tx) PutCredential(hash string, a Actor) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO credentials(hash,attempt,data) VALUES(?,?,?)", hash, a.Attempt, t.encode(a))
}

// Receipt is the stored result of a request key's fingerprint.
func (t *Tx) Receipt(key string) (receipt, bool) {
	return fetch[receipt](t, "SELECT data FROM receipts WHERE key=?", key)
}

// PutReceipt stores the result of a request key's fingerprint, written at
// at by the service's clock. A receipt that hands out a credential is its
// attempt's dispatch receipt.
func (t *Tx) PutReceipt(key string, r receipt, at time.Time) {
	t.mustWrite()
	attempt := ""
	if r.Response.Attempt != nil && r.Response.Credential != "" {
		attempt = r.Response.Attempt.ID
	}
	t.exec("INSERT INTO receipts(key,created,attempt,data) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET attempt=excluded.attempt, data=excluded.data",
		key, at.UTC().Format(createdLayout), attempt, t.encode(r))
}

// DispatchTokens are the credentials a dispatch of attempt id left in its
// receipts, for an interrupted dispatch to recover. They are server-side
// only and never part of an API response.
func (t *Tx) DispatchTokens(id string) []string {
	var tokens []string
	t.rows(func(rows *sql.Rows) error {
		var token string
		err := rows.Scan(&token)
		tokens = append(tokens, token)
		return err
	}, "SELECT json_extract(data, '$.response.credential') FROM receipts WHERE attempt=? ORDER BY key", id)
	return tokens
}

// QueueEntry is task's place in its repository's merge queue.
func (t *Tx) QueueEntry(task string) (QueueEntry, bool) {
	return fetch[QueueEntry](t, "SELECT data FROM queue WHERE task=?", task)
}

// QueueOf is repo's merge queue in order.
func (t *Tx) QueueOf(repo string) []QueueEntry {
	return fetchAll[QueueEntry](t, "SELECT data FROM queue WHERE repo=? ORDER BY seq, task", repo)
}

// QueueEntries are the entries of every repository's merge queue, by
// repository, each in its order.
func (t *Tx) QueueEntries() []QueueEntry {
	return fetchAll[QueueEntry](t, "SELECT data FROM queue ORDER BY repo, seq, task")
}

// PutQueueEntry stores e under its task, in its repository's merge queue.
func (t *Tx) PutQueueEntry(e QueueEntry) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO queue(task,repo,seq,data) VALUES(?,?,?,?)", e.Task, e.Repo, e.Seq, t.encode(e))
}

// DeleteQueueEntry takes task out of its repository's merge queue.
func (t *Tx) DeleteQueueEntry(task string) {
	t.mustWrite()
	t.exec("DELETE FROM queue WHERE task=?", task)
}
