package control

import (
	"fmt"
	"slices"
)

func (s Service) transition(st *State, a Actor, r Request) (Response, error) {
	item, ok := st.Items[r.ID]
	if !ok {
		return Response{}, problem(404, "unknown item")
	}
	intentOnly := r.Version == 0 && (r.Op == "run" || r.Op == "stop")
	if r.Version != item.Version && !intentOnly {
		return Response{}, problem(409, "version is %d", item.Version)
	}
	if item.Kind == "repo" {
		return Response{}, problem(400, "repository has no workflow state")
	}
	switch r.Op {
	case "approve":
		if err := operator(a); err != nil {
			return Response{}, err
		}
		if item.State != "prepare" {
			return Response{}, problem(409, "only a prepared item can be approved")
		}
		item.State = "approved"
		item.ApprovedVersion = item.Version
	case "run", "stop":
		if err := s.coordinate(st, a, &item, r.Op); err != nil {
			return Response{}, err
		}
	case "state":
		if !workerItem(st, a, item.ID) {
			return Response{}, problem(403, "cannot change another execution's state")
		}
		if err := stateChange(st, a, item, r.State); err != nil {
			return Response{}, err
		}
		item.State = r.State
		if r.Item.PR != "" {
			item.PR = r.Item.PR
		}
	}
	item.Version++
	st.Items[item.ID] = item
	st.update(a, r, r.Op, fmt.Sprintf("%s (version %d)", item.State, item.Version), s.now())
	return Response{Item: &item}, nil
}

// coordinate records run or stop intent; a stop records when coordination stopped.
func (s Service) coordinate(st *State, a Actor, item *Item, op string) error {
	if err := operator(a); err != nil {
		return err
	}
	if op == "run" && (!approved(st, *item) || item.State == "done") {
		return problem(409, "item requires approval before coordination")
	}
	if op == "stop" && item.Coordinating {
		item.StoppedAt = s.now()
	}
	item.Coordinating = op == "run"
	return nil
}

func approved(st *State, item Item) bool {
	if item.Plan != "" {
		item = st.Items[item.Plan]
	}
	return item.ApprovedVersion > 0 && item.State != "prepare"
}

func stateChange(st *State, a Actor, item Item, want string) error {
	if item.Kind == "task" && (want == "running" || want == "in-review") && !active(st, item) {
		return problem(409, "running state requires a current execution")
	}
	allowed := map[string][]string{
		"approved":    {"planned", "running", "failed"},
		"planned":     {"executing", "verifying", "blocked", "done"},
		"executing":   {"verifying", "blocked", "done"},
		"verifying":   {"executing", "blocked", "done"},
		"blocked":     {"executing", "verifying", "done"},
		"todo":        {"running", "failed"},
		"running":     {"in-review", "needs-human", "failed", "done"},
		"in-review":   {"running", "needs-human", "failed", "done"},
		"needs-human": {"running", "in-review", "failed"},
		"failed":      {"todo"},
	}
	if !slices.Contains(allowed[item.State], want) {
		return problem(409, "cannot move %s to %s", item.State, want)
	}
	if item.State == "failed" && a.Role != "operator" {
		return problem(403, "only operator can retry a failed task")
	}
	if item.State == "needs-human" && a.Role != "operator" && a.Role != "coordinator" {
		return problem(403, "operator must resolve the hard stop")
	}
	if want != "failed" && PendingQuestion(st, item.ID) > 0 {
		return problem(409, "operator question is unresolved")
	}
	if want == "done" && item.Kind == "plan" {
		for _, t := range st.Items {
			if t.Plan == item.ID && t.State != "done" {
				return problem(409, "plan has unfinished tasks")
			}
		}
	}
	return nil
}

func (s Service) reserve(st *State, a Actor, r Request) (Response, error) {
	if a.Role != "coordinator" && a.Role != "operator" {
		return Response{}, problem(403, "only coordinator or operator can dispatch")
	}
	item, ok := st.Items[r.ID]
	if !ok || item.Kind == "repo" {
		return Response{}, problem(404, "unknown task or plan")
	}
	if a.Role == "coordinator" && r.Version != item.Version {
		return Response{}, problem(409, "card changed since placement; version is %d", item.Version)
	}
	if PendingQuestion(st, item.ID) > 0 {
		return Response{}, problem(409, "operator question is unresolved")
	}
	if !approved(st, item) {
		return Response{}, problem(409, "item is not approved")
	}
	if a.Role == "coordinator" && !dispatchIntent(st, item) {
		return Response{}, problem(409, "coordination was stopped")
	}
	if active(st, item) {
		return Response{}, problem(409, "item already has an active execution")
	}
	if item.State == "done" || item.State == "failed" || item.State == "needs-human" && !Vanished(st, item) {
		return Response{}, problem(409, "item is not dispatchable")
	}
	for _, id := range item.Depends {
		if st.Items[id].State != "done" {
			return Response{}, problem(409, "blocked by %s", id)
		}
	}
	if r.Body == "" || r.State == "" {
		return Response{}, problem(400, "dispatch requires host and harness")
	}
	id := st.id("a")
	repo := st.Items[item.Repo]
	_, name, _ := cutSlug(repo.Slug)
	at := Attempt{ID: id, Item: item.ID, Host: r.Body, Harness: r.State, RunID: fmt.Sprintf("%s-%d-%s", name, Number(item.ID), id), State: "dispatching", Started: s.now()}
	secret := Secret()
	st.Auth[digest([]byte(secret))] = Actor{Name: id, Role: "worker", Attempt: id}
	st.Attempts[id] = at
	item.Attempt = id
	item.Version++
	if item.Kind == "task" {
		item.State = "running"
	}
	st.Items[item.ID] = item
	st.update(a, r, "dispatch", id, s.now())
	return Response{Item: &item, Attempt: &at, Credential: secret}, nil
}

func cutSlug(s string) (string, string, bool) {
	for i, c := range s {
		if c == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return "", s, false
}

// SetAttempt records a host observation. Unknown dispatch outcomes stay reserved.
func (s Service) SetAttempt(id, state, window string) error {
	return s.Store.Transaction(func(st *State) error {
		at, ok := st.Attempts[id]
		if !ok {
			return problem(404, "unknown execution")
		}
		if at.State == "ended" {
			if state == "ended" {
				return nil
			}
			return problem(409, "execution has ended")
		}
		if !slices.Contains([]string{"dispatching", "running", "ended"}, state) {
			return problem(400, "invalid execution state")
		}
		at.State = state
		if window != "" {
			at.Window = window
		}
		if state == "ended" {
			at.Ended = s.now()
		}
		st.Attempts[id] = at
		st.update(Actor{Name: "coordinator", Role: "coordinator"}, Request{ID: at.Item}, "execution", id+": "+state, s.now())
		return nil
	})
}

func dispatchIntent(st *State, item Item) bool {
	return item.Coordinating || item.Plan != "" && st.Items[item.Plan].Coordinating
}

// CheckDispatch rechecks durable intent before a reserved dispatch reaches a host.
func (s Service) CheckDispatch(id, attempt string) error {
	return s.Store.Transaction(func(st *State) error {
		item, ok := st.Items[id]
		if !ok || item.Attempt != attempt || st.Attempts[attempt].State != "dispatching" {
			return problem(409, "dispatch is no longer current")
		}
		if !dispatchIntent(st, item) {
			return problem(409, "coordination was stopped")
		}
		if PendingQuestion(st, item.ID) > 0 {
			return problem(409, "operator question is unresolved")
		}
		return nil
	})
}

func (s Service) mergeLock(st *State, a Actor, r Request) (Response, error) {
	if r.Op == "lock-recover" {
		return s.recoverLock(st, a, r)
	}
	if r.Op == "lock-show" {
		if err := operator(a); err != nil {
			return Response{}, err
		}
		lock, ok := st.Locks[r.ID]
		if !ok {
			return Response{}, problem(404, "repository has no merge lock")
		}
		return Response{Lock: &lock}, nil
	}
	at, ok := st.Attempts[r.Attempt]
	if !ok {
		return Response{}, problem(404, "unknown execution")
	}
	if a.Role == "worker" && a.Attempt != at.ID {
		return Response{}, problem(403, "lock belongs to another execution")
	}
	item := st.Items[at.Item]
	repo := item.Repo
	lock, held := st.Locks[repo]
	if r.Op == "lock-acquire" {
		return s.acquireLock(st, a, item, at)
	}
	if !held || lock.Attempt != at.ID || lock.Token != r.Token {
		return Response{}, problem(409, "merge lock ownership does not match")
	}
	if r.Op == "lock-check" {
		if PendingQuestion(st, item.ID) > 0 {
			return Response{}, problem(409, "operator question is unresolved")
		}
		if at.State == "ended" || !slices.Contains([]string{"running", "in-review"}, item.State) {
			return Response{}, problem(409, "execution is not permitted to merge")
		}
		return Response{Lock: &lock}, nil
	}
	delete(st.Locks, repo)
	st.update(a, Request{ID: item.ID}, "lock-released", at.ID, s.now())
	return Response{}, nil
}

func (s Service) acquireLock(st *State, a Actor, item Item, at Attempt) (Response, error) {
	if PendingQuestion(st, item.ID) > 0 {
		return Response{}, problem(409, "operator question is unresolved")
	}
	if item.Kind != "task" || at.State == "ended" || !slices.Contains([]string{"running", "in-review"}, item.State) {
		return Response{}, problem(409, "only an active task can take a merge lock")
	}
	lock, held := st.Locks[item.Repo]
	if held && lock.Attempt != at.ID {
		return Response{}, problem(409, "repository lock is held by %s", lock.Attempt)
	}
	if !held {
		lock = Lock{Repo: item.Repo, Attempt: at.ID, Token: Secret(), Acquired: s.now()}
		st.Locks[item.Repo] = lock
		st.update(a, Request{ID: item.ID}, "lock-acquired", at.ID, s.now())
	}
	return Response{Lock: &lock}, nil
}

func (s Service) recoverLock(st *State, a Actor, r Request) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	lock, ok := st.Locks[r.ID]
	if !ok {
		return Response{}, problem(404, "repository has no merge lock")
	}
	if r.Attempt != lock.Attempt || r.Token != lock.Token || r.Body == "" {
		return Response{}, problem(409, "recovery requires the current owner, token and reconciliation evidence")
	}
	if st.Attempts[lock.Attempt].State != "ended" {
		return Response{}, problem(409, "execution must be confirmed ended before lock recovery")
	}
	delete(st.Locks, r.ID)
	st.update(a, Request{ID: st.Attempts[lock.Attempt].Item}, "lock-recovered", r.Body, s.now())
	return Response{}, nil
}

func (s Service) endAttempt(st *State, a Actor, r Request) (Response, error) {
	at, ok := st.Attempts[r.ID]
	if !ok {
		return Response{}, problem(404, "unknown execution")
	}
	if at.State != "ended" {
		at = s.endDead(st, a, at, r.Body)
	}
	return Response{Attempt: &at}, nil
}
