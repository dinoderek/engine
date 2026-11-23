package control

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"
)

// Service applies commands within one database transaction.
type Service struct {
	Store *Store
	Now   func() time.Time
	// Reconcile verifies the host before an operator ends an execution.
	Reconcile func(string) error
	// Observe reads an attempt's cached host observation; nil reports unknown.
	Observe func(attempt string) Observation
	// Hosts reads the coordinator's latest pass over the hosts; nil reports none.
	Hosts func() HostStatus
}

// write runs fn in the store's one writing transaction.
func (s Service) write(fn func(*Tx) error) error {
	return s.Store.Write(context.Background(), fn)
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// readOps change no record: each runs on a view, beside the others.
var readOps = []string{"get", "list", "plan-view", "updates", "inbox", "attempts", "merge-queue", "session-view", "host-status"}

// Execute deduplicates successful writes by actor and caller-supplied request key.
func (s Service) Execute(a Actor, r Request) (Response, error) {
	checked := "" // the attempt's state the host check confirmed
	if r.Op == "attempt-reconcile" {
		var err error
		if checked, err = s.checkReconcile(a, r); err != nil {
			return Response{}, err
		}
	}
	var out Response
	write := !slices.Contains(readOps, r.Op)
	run := s.Store.Write
	if !write {
		run = s.Store.Read
	}
	err := run(context.Background(), func(tx *Tx) error {
		if err := authorizeExecution(tx, a); err != nil {
			return err
		}
		if r.Op == "attempt-reconcile" {
			if err := stillChecked(tx, r.ID, checked); err != nil {
				return err
			}
		}
		if write && (r.Key == "" || len(r.Key) > 200) {
			return problem(400, "a request key of 1–200 characters is required")
		}
		key := fingerprint([]string{a.Name, a.Attempt, r.Key})
		hash := fingerprint(digested(r))
		if prev, ok := tx.Receipt(key); write && ok {
			if prev.Digest != hash {
				return problem(409, "request key was already used for a different command")
			}
			out = prev.Response
			return nil
		}
		var err error
		out, err = s.apply(tx, a, r)
		if err != nil {
			return err
		}
		if write {
			tx.PutReceipt(key, receipt{Digest: hash, Response: out})
		}
		return nil
	})
	return out, err
}

// checkReconcile runs the operator's attempt reconcile's host check and
// returns the attempt's state as it stood before the check, which the check
// confirmed.
func (s Service) checkReconcile(a Actor, r Request) (string, error) {
	if err := operator(a); err != nil {
		return "", err
	}
	if s.Reconcile == nil || r.Body == "" {
		return "", &Problem{Status: 409, Reason: ReasonReconcileRefused, Message: "host reconciliation and evidence are required"}
	}
	var state string
	err := s.Store.Read(context.Background(), func(tx *Tx) error {
		at, _ := tx.Attempt(r.ID)
		state = at.State
		return nil
	})
	if err != nil {
		return "", err
	}
	return state, s.Reconcile(r.ID)
}

// stillChecked refuses a reconcile whose attempt moved on after its host
// check, such as a dispatch a coordinator pass launched meanwhile: the check
// never confirmed the state it is in now.
func stillChecked(tx *Tx, id, checked string) error {
	at, ok := tx.Attempt(id)
	if !ok || at.State == checked || at.State == "ended" {
		return nil
	}
	return &Problem{Status: 409, Reason: ReasonReconcileRefused,
		Message: "execution moved from " + checked + " to " + at.State + " while its host was checked; reconcile again"}
}

func authorizeExecution(tx *Tx, a Actor) error {
	if a.Role != "worker" {
		return nil
	}
	at, ok := tx.Attempt(a.Attempt)
	if item, _ := tx.Item(at.Item); !ok || at.State == "ended" || item.Attempt != at.ID {
		return problem(403, "execution credential is no longer active")
	}
	return nil
}

func (s Service) apply(tx *Tx, a Actor, r Request) (Response, error) {
	if err := questionFields(r); err != nil {
		return Response{}, err
	}
	if r.Move != nil && r.Op != "task-move" && r.Op != "host-move" {
		return Response{}, problem(400, "only task-move and host-move take a move")
	}
	switch r.Op {
	case "plan-view":
		return planView(tx, a, r.ID)
	case "session-view":
		return s.sessionView(tx, a)
	case "host-status":
		return s.hostStatus(a)
	case "get", "list", "attempts":
		return read(tx, a, r)
	case "updates", "inbox", "say", "ask", "reply", "ack", "resolve":
		return s.messages(tx, a, r)
	case "ack-all":
		return s.ackAll(tx, a, r)
	case "create":
		return s.create(tx, a, r)
	case "edit":
		return s.edit(tx, a, r)
	case "complete-external":
		return s.completeExternal(tx, a, r)
	case "approve", "run", "stop", "state":
		return s.transition(tx, a, r)
	case "claim":
		return s.reserve(tx, a, r)
	case "attempt-reconcile":
		return s.endAttempt(tx, a, r)
	case "task-stop":
		return s.stopTask(tx, a, r)
	case "task-resume":
		return s.resumeTask(tx, a, r)
	case "task-move":
		return s.moveTask(tx, a, r)
	case "host-pause", "host-resume":
		return s.pauseHost(tx, a, r)
	case "host-move":
		return s.moveHost(tx, a, r)
	case "merge-request":
		return s.mergeRequest(tx, a, r)
	case "merge-queue":
		return mergeQueue(tx, a, r.ID)
	case "cleaned":
		return s.cleaned(tx, a, r)
	case "pane-input":
		return s.paneInput(tx, a, r)
	case "upgrade":
		return s.upgrade(tx, a, r)
	default:
		return Response{}, problem(400, "unknown operation %q", r.Op)
	}
}

// get is one item, with its merge queue entry if it has one.
func get(tx *Tx, id string) (Response, error) {
	item, ok := tx.Item(id)
	if !ok {
		return Response{}, problem(404, "unknown item %s", id)
	}
	item = withStatus(tx, item)
	out := Response{Item: &item}
	if e, queued := tx.QueueEntry(item.ID); queued {
		out.Queue = []QueueEntry{e}
	}
	return out, nil
}

func read(tx *Tx, a Actor, r Request) (Response, error) {
	if r.ID != "" && !readable(tx, a, r.ID) {
		return Response{}, problem(403, "item is outside this execution's read scope")
	}
	if r.Op == "get" {
		return get(tx, r.ID)
	}
	out := Response{}
	if r.Op == "attempts" {
		for _, at := range tx.AttemptsFor(r.ID) {
			if readable(tx, a, at.Item) {
				out.Attempts = append(out.Attempts, at)
			}
		}
		// Oldest first; attempts started in the same instant by their number.
		slices.SortFunc(out.Attempts, func(a, b Attempt) int { return cmp.Or(a.Started.Compare(b.Started), Number(a.ID)-Number(b.ID)) })
		return out, nil
	}
	for _, item := range tx.ItemsByKindUnder(r.State, r.ID) {
		if readable(tx, a, item.ID) {
			out.Items = append(out.Items, withStatus(tx, item))
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return Number(out.Items[i].ID) < Number(out.Items[j].ID) })
	return out, nil
}

func workerItem(tx *Tx, a Actor, id string) bool {
	if a.Role == "operator" || a.Role == "coordinator" {
		return true
	}
	at := tx.attempt(a.Attempt)
	owner := tx.item(at.Item)
	item := tx.item(id)
	return id == owner.ID || owner.Kind == "plan" && item.Plan == owner.ID
}

func operator(a Actor) error {
	if a.Role != "operator" {
		return problem(403, "this operation requires the operator")
	}
	return nil
}

func (s Service) create(tx *Tx, a Actor, r Request) (Response, error) {
	item := r.Item
	if strings.TrimSpace(item.Title) == "" {
		return Response{}, problem(400, "title is required")
	}
	if item.ID != "" || item.Version != 0 || item.Attempt != "" || item.Coordinating || item.ApprovedVersion != 0 || item.State != "" || item.Status != "" || item.Question != nil || item.Held {
		return Response{}, problem(400, "server-owned fields cannot be set on creation")
	}
	if err := validateCreation(tx, a, item); err != nil {
		return Response{}, err
	}
	item.ID = tx.NextID(map[string]string{"repo": "r", "plan": "p", "task": "t"}[item.Kind])
	item.Version = 1
	item.State = map[string]string{"repo": "active", "plan": "prepare", "task": "todo"}[item.Kind]
	tx.PutItem(item)
	if err := validateGraph(tx, item); err != nil {
		return Response{}, err
	}
	r.ID = item.ID
	tx.AppendUpdate(a, r, "created", item.Title, s.now())
	return Response{Item: &item}, nil
}

func validateCreation(tx *Tx, a Actor, item Item) error {
	switch item.Kind {
	case "repo":
		if err := operator(a); err != nil {
			return err
		}
		if !validSlug(item.Slug) || item.Remote == "" || strings.HasPrefix(item.Remote, "-") {
			return problem(400, "repository requires an owner/name slug and a remote")
		}
		if _, taken := tx.RepoBySlug(item.Slug); taken {
			return problem(409, "repository slug already exists")
		}
	case "plan", "task":
		return validateWork(tx, a, item)
	default:
		return problem(400, "kind must be repo, plan or task")
	}
	return nil
}

// validateWork checks a new plan or task: a task belongs to an unfinished
// plan in its repository, and a worker creates only its own plan's tasks.
func validateWork(tx *Tx, a Actor, item Item) error {
	repo, ok := tx.Item(item.Repo)
	if !ok || repo.Kind != "repo" {
		return problem(400, "unknown repository")
	}
	if item.Kind == "task" && item.Plan == "" {
		return problem(400, "a task requires its plan; build one task outside a plan with the build skill")
	}
	if item.Plan != "" {
		parent, ok := tx.Item(item.Plan)
		if !ok || parent.Kind != "plan" || parent.Repo != item.Repo || item.Kind != "task" || parent.State == "done" {
			return problem(400, "invalid parent plan")
		}
	}
	if a.Role == "worker" && (item.Kind != "task" || !workerItem(tx, a, item.Plan)) {
		return problem(403, "worker can only create tasks in its own plan")
	}
	return nil
}

func (s Service) edit(tx *Tx, a Actor, r Request) (Response, error) {
	old, ok := tx.Item(r.ID)
	if !ok {
		return Response{}, problem(404, "unknown item")
	}
	if !workerItem(tx, a, r.ID) {
		return Response{}, problem(403, "cannot edit another execution's item")
	}
	if old.Kind == "repo" {
		return Response{}, problem(400, "repository identity is immutable")
	}
	if r.Version != old.Version {
		return Response{}, problem(409, "version is %d", old.Version)
	}
	if active(tx, old) {
		return Response{}, problem(409, "stop the active execution before editing its card")
	}
	item := r.Item
	if strings.TrimSpace(item.Title) == "" {
		return Response{}, problem(400, "title is required")
	}
	// Identity, approval and execution state cannot be replaced by a card edit.
	old.Title, old.Body, old.Depends, old.Requires = item.Title, item.Body, item.Depends, item.Requires
	old.Harness, old.Tier = item.Harness, item.Tier
	old.Version++
	if old.Kind == "plan" {
		old.ApprovedVersion = 0
		old.State = "prepare"
		old.Coordinating = false
	}
	tx.PutItem(old)
	if err := validateGraph(tx, old); err != nil {
		return Response{}, err
	}
	tx.AppendUpdate(a, r, "edited", old.Body, s.now())
	return Response{Item: &old}, nil
}

func active(tx *Tx, item Item) bool {
	at, ok := tx.Attempt(item.Attempt)
	if ok && at.State != "ended" {
		return true
	}
	if item.Kind == "plan" {
		for _, child := range tx.ItemsOfPlan(item.ID) {
			at, ok = tx.Attempt(child.Attempt)
			if ok && at.State != "ended" {
				return true
			}
		}
	}
	return false
}

func validSlug(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return false
		}
		for _, c := range p {
			if !slugCharacter(c) {
				return false
			}
		}
	}
	return true
}

func slugCharacter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

// Number is the unique numeric identity shared with host worktree naming.
func Number(id string) int {
	var n int
	if len(id) > 1 {
		for _, c := range id[1:] {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func validateGraph(tx *Tx, item Item) error {
	if item.Kind != "task" && len(item.Depends) > 0 {
		return problem(400, "only tasks have dependencies")
	}
	seen := map[string]bool{}
	stack := slices.Clone(item.Depends)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == item.ID {
			return problem(400, "dependency cycle")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		dep, ok := tx.Item(id)
		if !ok || dep.Kind != "task" || dep.Repo != item.Repo {
			return problem(400, "dependency must be a task in this repository")
		}
		stack = append(stack, dep.Depends...)
	}
	if item.Tier != "" && !slices.Contains([]string{"deep", "standard", "light"}, item.Tier) {
		return problem(400, "invalid tier")
	}
	return nil
}

// Encode is a JSON value for a CLI or test.
func Encode(v any) string { b, _ := json.Marshal(v); return string(b) }
