package control

import (
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
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Execute deduplicates successful writes by actor and caller-supplied request key.
func (s Service) Execute(a Actor, r Request) (Response, error) {
	if r.Op == "attempt-reconcile" {
		if err := operator(a); err != nil {
			return Response{}, err
		}
		if s.Reconcile == nil || r.Body == "" {
			return Response{}, problem(409, "host reconciliation and evidence are required")
		}
		if err := s.Reconcile(r.ID); err != nil {
			return Response{}, err
		}
	}
	var out Response
	err := s.Store.Transaction(func(st *State) error {
		if err := authorizeExecution(st, a); err != nil {
			return err
		}
		write := !slices.Contains([]string{"get", "list", "plan-view", "updates", "inbox", "attempts", "merge-queue", "session-view"}, r.Op)
		if write && (r.Key == "" || len(r.Key) > 200) {
			return problem(400, "a request key of 1–200 characters is required")
		}
		key := fingerprint([]string{a.Name, a.Attempt, r.Key})
		hash := fingerprint(r)
		if prev, ok := st.Receipts[key]; write && ok {
			if prev.Digest != hash {
				return problem(409, "request key was already used for a different command")
			}
			out = prev.Response
			return nil
		}
		var err error
		out, err = s.apply(st, a, r)
		if err != nil {
			return err
		}
		if write {
			st.Receipts[key] = receipt{Digest: hash, Response: out}
		}
		return nil
	})
	return out, err
}

func authorizeExecution(st *State, a Actor) error {
	if a.Role != "worker" {
		return nil
	}
	at, ok := st.Attempts[a.Attempt]
	if !ok || at.State == "ended" || st.Items[at.Item].Attempt != at.ID {
		return problem(403, "execution credential is no longer active")
	}
	return nil
}

func (s Service) apply(st *State, a Actor, r Request) (Response, error) {
	switch r.Op {
	case "plan-view":
		return planView(st, a, r.ID)
	case "session-view":
		return s.sessionView(st, a)
	case "get", "list", "attempts":
		return read(st, a, r)
	case "create":
		return s.create(st, a, r)
	case "edit":
		return s.edit(st, a, r)
	case "complete-external":
		return s.completeExternal(st, a, r)
	case "approve", "run", "stop", "state":
		return s.transition(st, a, r)
	case "updates", "inbox", "say", "ask", "reply", "ack", "resolve":
		return s.messages(st, a, r)
	case "claim":
		return s.reserve(st, a, r)
	case "attempt-reconcile":
		return s.endAttempt(st, a, r)
	case "merge-request":
		return s.mergeRequest(st, a, r)
	case "merge-queue":
		return mergeQueue(st, a, r.ID)
	default:
		return Response{}, problem(400, "unknown operation %q", r.Op)
	}
}

// get is one item, with its merge queue entry if it has one.
func get(st *State, id string) (Response, error) {
	item, ok := st.Items[id]
	if !ok {
		return Response{}, problem(404, "unknown item %s", id)
	}
	item = withStatus(st, item)
	out := Response{Item: &item}
	if e, queued := st.Queue[item.ID]; queued {
		out.Queue = []QueueEntry{e}
	}
	return out, nil
}

func read(st *State, a Actor, r Request) (Response, error) {
	if r.ID != "" && !readable(st, a, r.ID) {
		return Response{}, problem(403, "item is outside this execution's read scope")
	}
	if r.Op == "get" {
		return get(st, r.ID)
	}
	out := Response{}
	if r.Op == "attempts" {
		for _, at := range st.Attempts {
			if !readable(st, a, at.Item) {
				continue
			}
			if r.ID == "" || at.Item == r.ID {
				out.Attempts = append(out.Attempts, at)
			}
		}
		sort.Slice(out.Attempts, func(i, j int) bool { return out.Attempts[i].Started.Before(out.Attempts[j].Started) })
		return out, nil
	}
	for _, item := range st.Items {
		if !readable(st, a, item.ID) {
			continue
		}
		if r.State != "" && item.Kind != r.State {
			continue
		}
		if r.ID != "" && item.Repo != r.ID && item.Plan != r.ID {
			continue
		}
		out.Items = append(out.Items, withStatus(st, item))
	}
	sort.Slice(out.Items, func(i, j int) bool { return Number(out.Items[i].ID) < Number(out.Items[j].ID) })
	return out, nil
}

func workerItem(st *State, a Actor, id string) bool {
	if a.Role == "operator" || a.Role == "coordinator" {
		return true
	}
	at := st.Attempts[a.Attempt]
	owner := st.Items[at.Item]
	item := st.Items[id]
	return id == owner.ID || owner.Kind == "plan" && item.Plan == owner.ID
}

func operator(a Actor) error {
	if a.Role != "operator" {
		return problem(403, "this operation requires the operator")
	}
	return nil
}

func (s Service) create(st *State, a Actor, r Request) (Response, error) {
	item := r.Item
	if strings.TrimSpace(item.Title) == "" {
		return Response{}, problem(400, "title is required")
	}
	if item.ID != "" || item.Version != 0 || item.Attempt != "" || item.Coordinating || item.ApprovedVersion != 0 || item.State != "" || item.Status != "" {
		return Response{}, problem(400, "server-owned fields cannot be set on creation")
	}
	if err := validateCreation(st, a, item); err != nil {
		return Response{}, err
	}
	item.ID = st.id(map[string]string{"repo": "r", "plan": "p", "task": "t"}[item.Kind])
	item.Version = 1
	item.State = map[string]string{"repo": "active", "plan": "prepare", "task": "todo"}[item.Kind]
	st.Items[item.ID] = item
	if err := validateGraph(st, item); err != nil {
		return Response{}, err
	}
	r.ID = item.ID
	st.update(a, r, "created", item.Title, s.now())
	return Response{Item: &item}, nil
}

func validateCreation(st *State, a Actor, item Item) error {
	switch item.Kind {
	case "repo":
		if err := operator(a); err != nil {
			return err
		}
		if !validSlug(item.Slug) || item.Remote == "" || strings.HasPrefix(item.Remote, "-") {
			return problem(400, "repository requires an owner/name slug and a remote")
		}
		for _, v := range st.Items {
			if v.Kind == "repo" && v.Slug == item.Slug {
				return problem(409, "repository slug already exists")
			}
		}
	case "plan", "task":
		return validateWork(st, a, item)
	default:
		return problem(400, "kind must be repo, plan or task")
	}
	return nil
}

// validateWork checks a new plan or task: a task belongs to an unfinished
// plan in its repository, and a worker creates only its own plan's tasks.
func validateWork(st *State, a Actor, item Item) error {
	repo, ok := st.Items[item.Repo]
	if !ok || repo.Kind != "repo" {
		return problem(400, "unknown repository")
	}
	if item.Kind == "task" && item.Plan == "" {
		return problem(400, "a task requires its plan; build one task outside a plan with the build skill")
	}
	if item.Plan != "" {
		parent, ok := st.Items[item.Plan]
		if !ok || parent.Kind != "plan" || parent.Repo != item.Repo || item.Kind != "task" || parent.State == "done" {
			return problem(400, "invalid parent plan")
		}
	}
	if a.Role == "worker" && (item.Kind != "task" || !workerItem(st, a, item.Plan)) {
		return problem(403, "worker can only create tasks in its own plan")
	}
	return nil
}

func (s Service) edit(st *State, a Actor, r Request) (Response, error) {
	old, ok := st.Items[r.ID]
	if !ok {
		return Response{}, problem(404, "unknown item")
	}
	if !workerItem(st, a, r.ID) {
		return Response{}, problem(403, "cannot edit another execution's item")
	}
	if old.Kind == "repo" {
		return Response{}, problem(400, "repository identity is immutable")
	}
	if r.Version != old.Version {
		return Response{}, problem(409, "version is %d", old.Version)
	}
	if active(st, old) {
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
	st.Items[old.ID] = old
	if err := validateGraph(st, old); err != nil {
		return Response{}, err
	}
	st.update(a, r, "edited", old.Body, s.now())
	return Response{Item: &old}, nil
}

func active(st *State, item Item) bool {
	at, ok := st.Attempts[item.Attempt]
	if ok && at.State != "ended" {
		return true
	}
	if item.Kind == "plan" {
		for _, child := range st.Items {
			if child.Plan == item.ID {
				at, ok = st.Attempts[child.Attempt]
				if ok && at.State != "ended" {
					return true
				}
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

func validateGraph(st *State, item Item) error {
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
		dep, ok := st.Items[id]
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
