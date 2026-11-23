package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"
)

// legacyLock is the part of a version 1 lock record the migration reads.
type legacyLock struct {
	Repo    string `json:"repo"`
	Attempt string `json:"attempt"`
}

// LockRetired tells an executor that merge locks are gone.
const LockRetired = "lock-retired"

const retiredText = "Merge locks are retired: the coordinator's merge queue orders merges. Never run engine lock. " +
	"Once your PR is in-review, reviewed and pushed, run engine task merge-ready and end your turn; a merge request you already made is queued."

// backupSuffix names the copy of a records-table database taken before it
// migrates: the database path plus this.
var backupSuffix = fmt.Sprintf(".pre-v%d", schemaVersion)

// migrate brings a records-table database, versions 0 to 6, to the current
// version. It first writes an owner-only copy beside it (backupSuffix), and
// refuses to start when that file exists. Then, in one transaction, plans
// move to their stored states, every record becomes a row of its table,
// every update's item snapshot is slimmed, and the records table goes. From
// version 1 or 2 each unfinished task's unanswered merge request from its
// current attempt also joins the queue in request order, and the executor
// of every lock and every active task is told.
func (s *Store) migrate(version int) error {
	backup := s.path + backupSuffix
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("control database migration backup %s already exists: move it away to migrate", backup)
	}
	if err := s.vacuumInto(backup); err != nil {
		return fmt.Errorf("control database migration backup %s: %w", backup, err)
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := checkRecords(tx); err != nil {
			return err
		}
		locks, err := legacyLocks(tx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(migratePlanStates + telegramSchema + schema + copyRecords); err != nil {
			return fmt.Errorf("control database migration: %w", err)
		}
		if version == 1 || version == 2 {
			t := &Tx{ctx: context.Background(), q: tx, write: true}
			retire(t, locks, time.Now().UTC())
			if t.err != nil {
				return t.err
			}
		}
		_, err = tx.Exec(fmt.Sprintf("DROP TABLE records; PRAGMA user_version=%d;", schemaVersion))
		return err
	})
}

// migratePlanStates moves a plan stored in a state version 1 allowed, and
// later versions derive, back to planned.
const migratePlanStates = "UPDATE records SET data = json_set(data, '$.state', 'planned') " +
	"WHERE key LIKE 'item/%' AND json_extract(data, '$.kind') = 'plan' " +
	"AND json_extract(data, '$.state') IN ('executing', 'verifying', 'blocked'); "

// recordKinds are the record key prefixes and single keys a records table
// holds; lock records are version 1's.
const recordKinds = "substr(key, 1, instr(key || '/', '/')) IN ('item/', 'attempt/', 'update/', 'queue/', 'auth/', 'receipt/', 'host/', 'lock/') " +
	"OR key IN ('next', 'pass', 'upgrade')"

// checkRecords refuses a records table with a record of no known kind, or
// one whose data is not JSON.
func checkRecords(tx *sql.Tx) error {
	var key string
	err := tx.QueryRow("SELECT key FROM records WHERE NOT (" + recordKinds + ") OR NOT json_valid(data) ORDER BY key LIMIT 1").Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("control record %s is unknown or not JSON", key)
	}
	return err
}

func legacyLocks(tx *sql.Tx) ([]legacyLock, error) {
	rows, err := tx.Query("SELECT key, data FROM records WHERE key LIKE 'lock/%' ORDER BY key")
	if err != nil {
		return nil, err
	}
	var locks []legacyLock
	for rows.Next() && err == nil {
		var key, data string
		var l legacyLock
		if err = rows.Scan(&key, &data); err == nil {
			if err = json.Unmarshal([]byte(data), &l); err != nil {
				err = fmt.Errorf("control record %s: %w", key, err)
			}
		}
		locks = append(locks, l)
	}
	return locks, errors.Join(err, rows.Err(), rows.Close())
}

// copyRecords copies every record into its version 7 row: the key's ID, the
// record's queried fields as columns, and the record as data.
const copyRecords = `
INSERT INTO items(id,kind,repo,plan,state,coordinating,attempt,slug,version,data)
 SELECT substr(key, 6), json_extract(data, '$.kind'), COALESCE(json_extract(data, '$.repo'), ''),
  COALESCE(json_extract(data, '$.plan'), ''), COALESCE(json_extract(data, '$.state'), ''),
  COALESCE(json_extract(data, '$.coordinating'), 0), COALESCE(json_extract(data, '$.attempt'), ''),
  COALESCE(json_extract(data, '$.slug'), ''), COALESCE(json_extract(data, '$.version'), 0), data
 FROM records WHERE substr(key, 1, 5) = 'item/';
INSERT OR IGNORE INTO item_deps(item,depends)
 SELECT substr(r.key, 6), d.value FROM records r, json_each(r.data, '$.depends') d WHERE substr(r.key, 1, 5) = 'item/';
INSERT OR IGNORE INTO item_requires(item,cap)
 SELECT substr(r.key, 6), c.value FROM records r, json_each(r.data, '$.requires') c WHERE substr(r.key, 1, 5) = 'item/';
INSERT INTO attempts(id,item,state,host,data)
 SELECT substr(key, 9), COALESCE(json_extract(data, '$.item'), ''), COALESCE(json_extract(data, '$.state'), ''),
  COALESCE(json_extract(data, '$.host'), ''), data
 FROM records WHERE substr(key, 1, 8) = 'attempt/';
INSERT INTO updates(id,item,kind,author,reply_to,recipient,task,requires_reply,data)
 SELECT CAST(substr(key, 8) AS INTEGER), COALESCE(json_extract(data, '$.item'), ''), COALESCE(json_extract(data, '$.kind'), ''),
  COALESCE(json_extract(data, '$.author'), ''), COALESCE(json_extract(data, '$.reply_to'), 0),
  COALESCE(json_extract(data, '$.recipient'), ''), COALESCE(json_extract(data, '$.task'), ''),
  COALESCE(json_extract(data, '$.requires_reply'), 0),
  CASE WHEN json_type(data, '$.snapshot') = 'object' THEN json_set(data, '$.snapshot', json_object(` + snapshotFields + `)) ELSE data END
 FROM records WHERE substr(key, 1, 7) = 'update/';
INSERT INTO queue(task,repo,seq,data)
 SELECT substr(key, 7), COALESCE(json_extract(data, '$.repo'), ''), COALESCE(json_extract(data, '$.seq'), 0), data
 FROM records WHERE substr(key, 1, 6) = 'queue/';
INSERT INTO credentials(hash,attempt,data)
 SELECT substr(key, 6), COALESCE(json_extract(data, '$.attempt'), ''), data FROM records WHERE substr(key, 1, 5) = 'auth/';
INSERT INTO receipts(key,attempt,data)
 SELECT substr(key, 9), CASE WHEN COALESCE(json_extract(data, '$.response.credential'), '') != ''
  THEN COALESCE(json_extract(data, '$.response.attempt.id'), '') ELSE '' END, data
 FROM records WHERE substr(key, 1, 8) = 'receipt/';
INSERT INTO hosts(name,data) SELECT substr(key, 6), data FROM records WHERE substr(key, 1, 5) = 'host/';
UPDATE counters SET value = COALESCE((SELECT CAST(data AS INTEGER) FROM records WHERE key = 'next'), value) WHERE name = 'next';
INSERT INTO marks(name,data) SELECT key, data FROM records WHERE key IN ('pass', 'upgrade');
`

func retire(tx *Tx, locks []legacyLock, now time.Time) {
	coordinator := Actor{Name: "coordinator", Role: "coordinator"}
	tasks := tx.ItemsByKindUnder("task", "")
	for _, item := range tasks {
		if !slices.Contains([]string{"running", "in-review", "needs-human"}, item.State) {
			continue
		}
		if u, ok := legacyRequest(tx, item); ok {
			tx.PutQueueEntry(QueueEntry{Task: item.ID, Repo: item.Repo, Request: u.ID, PR: u.Merge.PR, Head: u.Merge.Head, Seq: u.ID, Phase: QueueWaiting})
		}
	}
	var told []string
	for _, l := range locks {
		if id := tx.attempt(l.Attempt).Item; id != "" && !slices.Contains(told, id) {
			told = append(told, id)
		}
	}
	for _, item := range tasks {
		at, ok := tx.Attempt(item.Attempt)
		if ok && at.State != "ended" && slices.Contains([]string{"running", "in-review"}, item.State) && !slices.Contains(told, item.ID) {
			told = append(told, item.ID)
		}
	}
	slices.SortFunc(told, func(a, b string) int { return Number(a) - Number(b) })
	for _, id := range told {
		tx.AppendUpdate(coordinator, Request{ID: id}, LockRetired, retiredText, now)
	}
}

// legacyRequest is version 1's pending merge request: the task's newest from
// its current attempt, unanswered.
func legacyRequest(tx *Tx, item Item) (Update, bool) {
	for u := range tx.UpdatesAboutNewestFirst(item.ID) {
		if u.Item != item.ID || u.Kind != "merge-request" || u.Merge == nil {
			continue
		}
		if u.Author != item.Attempt || tx.reply(u.ID, "'"+MergeMerged+"','"+MergeRefused+"'") > 0 {
			return Update{}, false
		}
		return u, true
	}
	return Update{}, false
}
