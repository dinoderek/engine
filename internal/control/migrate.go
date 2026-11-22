package control

import (
	"fmt"
	"slices"
	"time"
)

// schemaVersion is the control database's schema. Version 2 stores only
// prepare, approved, planned and done for a plan. Versions 1 and 2 held
// repository merge locks; version 3 holds the merge queue instead. Version
// 4 adds a question's choices, recommendation and class; a version 3
// database needs no rewrite. Version 5 adds Telegram delivery and poll state.
// Version 6 adds each host's saved tmux start time and an attempt's host-wide
// loss mark; a version 5 database needs no rewrite. An older server refuses
// the newer database rather than drop those fields.
const schemaVersion = 6

// legacyLock is the part of a version 1 lock record the migration reads.
type legacyLock struct {
	Repo    string `json:"repo"`
	Attempt string `json:"attempt"`
}

// LockRetired tells an executor that merge locks are gone.
const LockRetired = "lock-retired"

const retiredText = "Merge locks are retired: the coordinator's merge queue orders merges. Never run engine lock. " +
	"Once your PR is in-review, reviewed and pushed, run engine task merge-ready and end your turn; a merge request you already made is queued."

// migrate brings a version 1 or 2 database to the current version in one
// transaction: plans move to their stored states, each
// unfinished task's unanswered merge request from its current attempt joins
// the queue in request order, every lock record is deleted, and the executor
// of every lock and every active task is told.
func (s *Store) migrate() error {
	st, locks, err := s.load()
	if err != nil {
		return err
	}
	work := st.clone()
	retire(&work, locks, time.Now().UTC())
	changes, err := diff(&st, &work)
	if err != nil {
		return err
	}
	for _, l := range locks {
		changes = append(changes, change{key: "lock/" + l.Repo, del: true})
	}
	return s.write(changes, migratePlanStates+telegramSchema+fmt.Sprintf("PRAGMA user_version=%d;\n", schemaVersion))
}

func retire(st *State, locks []legacyLock, now time.Time) {
	coordinator := Actor{Name: "coordinator", Role: "coordinator"}
	for _, item := range st.Items {
		if item.Kind != "task" || !slices.Contains([]string{"running", "in-review", "needs-human"}, item.State) {
			continue
		}
		if u, ok := legacyRequest(st, item); ok {
			st.Queue[item.ID] = QueueEntry{Task: item.ID, Repo: item.Repo, Request: u.ID, PR: u.Merge.PR, Head: u.Merge.Head, Seq: u.ID, Phase: QueueWaiting}
		}
	}
	var told []string
	for _, l := range locks {
		if id := st.Attempts[l.Attempt].Item; id != "" && !slices.Contains(told, id) {
			told = append(told, id)
		}
	}
	for _, item := range st.Items {
		at, ok := st.Attempts[item.Attempt]
		if item.Kind == "task" && ok && at.State != "ended" && slices.Contains([]string{"running", "in-review"}, item.State) && !slices.Contains(told, item.ID) {
			told = append(told, item.ID)
		}
	}
	slices.SortFunc(told, func(a, b string) int { return Number(a) - Number(b) })
	for _, id := range told {
		st.update(coordinator, Request{ID: id}, LockRetired, retiredText, now)
	}
}

// legacyRequest is version 1's pending merge request: the task's newest from
// its current attempt, unanswered.
func legacyRequest(st *State, item Item) (Update, bool) {
	for i := len(st.Updates) - 1; i >= 0; i-- {
		u := st.Updates[i]
		if u.Item != item.ID || u.Kind != "merge-request" || u.Merge == nil {
			continue
		}
		if u.Author != item.Attempt || answered(st, u.ID) {
			return Update{}, false
		}
		return u, true
	}
	return Update{}, false
}

func answered(st *State, id int) bool {
	for _, u := range st.Updates[min(id, len(st.Updates)):] {
		if u.ReplyTo == id && (u.Kind == MergeMerged || u.Kind == MergeRefused) {
			return true
		}
	}
	return false
}
