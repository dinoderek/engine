package control

import "fmt"

func resolved(st *State, id int) bool {
	for _, u := range st.Updates {
		if u.ReplyTo == id && u.Kind == "resolved" {
			return true
		}
	}
	return false
}

func acknowledged(st *State, id int) bool {
	for _, u := range st.Updates {
		if u.ReplyTo == id && u.Kind == "acknowledged" {
			return true
		}
	}
	return false
}

func (s Service) messages(st *State, a Actor, r Request) (Response, error) {
	if r.Op == "updates" || r.Op == "inbox" {
		if r.Op == "inbox" && a.Role == "worker" {
			return Response{}, problem(403, "operator inbox requires operator access")
		}
		return messageListing(st, a, r), nil
	}
	if err := messageTarget(st, a, r); err != nil {
		return Response{}, err
	}
	kind, asker := r.Op, ""
	switch r.Op {
	case "ack", "resolve", "reply":
		var err error
		r, kind, err = replyCommand(st, a, r)
		if err != nil {
			return Response{}, err
		}
	case "ask":
		r.Recipient = "operator"
		r.RequiresReply = true
		var err error
		if asker, err = pauseForQuestion(st, a, r.ID); err != nil {
			return Response{}, err
		}
	case "say":
		if r.RequiresReply {
			return Response{}, problem(400, "use ask for a response requirement")
		}
	}
	if kind != "acknowledged" && r.Body == "" {
		return Response{}, problem(400, "message body is required")
	}
	if r.Recipient != "" && r.Recipient != "operator" && r.Recipient != "task" {
		return Response{}, problem(400, "recipient must be operator or task")
	}
	u := st.update(a, r, kind, r.Body, s.now())
	if asker != "" {
		u.Task = asker
		st.Updates[u.ID-1] = u
	}
	out := Response{Updates: []Update{u}}
	if kind == "resolved" {
		if restored, ok := s.release(st, a, st.Updates[r.ReplyTo-1]); ok {
			out.Updates = append(out.Updates, restored)
		}
	}
	return out, nil
}

// messageTarget checks the item a message is about and where it may publish.
func messageTarget(st *State, a Actor, r Request) error {
	if r.ID != "" {
		if _, ok := st.Items[r.ID]; !ok {
			return problem(404, "unknown item")
		}
		if a.Role == "worker" && st.Items[r.ID].Repo != st.Items[st.Attempts[a.Attempt].Item].Repo {
			return problem(403, "cannot message another repository")
		}
	}
	if r.Item.Plan != "" {
		return problem(400, "a message takes no plan; ask on the plan itself")
	}
	return nil
}

func messageListing(st *State, a Actor, r Request) Response {
	out := Response{}
	for _, u := range st.Updates {
		if !readableUpdate(st, a, u) {
			continue
		}
		if u.ID <= r.After || r.ID != "" && u.Item != r.ID {
			continue
		}
		if r.Op == "inbox" && (u.Recipient != "operator" || resolved(st, u.ID) || !u.RequiresReply && acknowledged(st, u.ID)) {
			continue
		}
		out.Updates = append(out.Updates, u)
		if len(out.Updates) == 200 {
			break
		}
	}
	return out
}

func replyCommand(st *State, a Actor, r Request) (Request, string, error) {
	if r.ReplyTo < 1 || r.ReplyTo > len(st.Updates) {
		return r, "", problem(404, "unknown update")
	}
	parent := st.Updates[r.ReplyTo-1]
	if parent.Recipient == "operator" {
		if err := operator(a); err != nil {
			return r, "", err
		}
	}
	if parent.Item != "" && !workerItem(st, a, parent.Item) {
		return r, "", problem(403, "cannot reply for another execution")
	}
	r.ID = parent.Item
	kind := r.Op
	if r.Op == "ack" {
		kind = "acknowledged"
	}
	if r.Op == "resolve" {
		if err := operator(a); err != nil {
			return r, "", err
		}
		if r.Body == "" {
			return r, "", problem(400, "resolution must record the decision")
		}
		kind = "resolved"
	}
	return r, kind, nil
}

// pauseForQuestion holds the task a question stops in needs-human: the task
// asked on, or the asking task when a task's session asks on its own plan.
// It returns that asking task, which the plan question records.
func pauseForQuestion(st *State, a Actor, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	held, asker := id, ""
	owner := st.Items[st.Attempts[a.Attempt].Item]
	if a.Role == "worker" && owner.Kind == "task" && owner.Plan != "" && owner.Plan == id {
		held, asker = owner.ID, owner.ID
	} else if !workerItem(st, a, id) {
		return "", problem(403, "cannot stop another execution")
	}
	item := st.Items[held]
	if item.Kind != "task" {
		return "", nil
	}
	if item.State != "running" && item.State != "in-review" && item.State != "needs-human" {
		return "", problem(409, "task is not active")
	}
	item.State = "needs-human"
	item.Version++
	st.Items[held] = item
	return asker, nil
}

// release restores the task a resolved question held to the state it had
// before needs-human, in the resolution's transaction. It leaves the task
// alone while another question holds it, once its state changed meanwhile,
// or when its execution ended: the coordinator then resumes it.
func (s Service) release(st *State, a Actor, question Update) (Update, bool) {
	id := question.Task
	if id == "" {
		id = question.Item
	}
	item, ok := st.Items[id]
	if !ok || item.Kind != "task" || item.State != "needs-human" || !active(st, item) || PendingQuestion(st, id) > 0 {
		return Update{}, false
	}
	prior := heldFrom(st, id)
	if prior == "" {
		return Update{}, false
	}
	item.State = prior
	item.Version++
	st.Items[id] = item
	return st.update(a, Request{ID: id}, "state", fmt.Sprintf("%s (version %d)", item.State, item.Version), s.now()), true
}

// heldFrom is the state a needs-human task had before it was held: the state
// in its newest update snapshot that is not needs-human.
func heldFrom(st *State, id string) string {
	for i := len(st.Updates) - 1; i >= 0; i-- {
		u := st.Updates[i]
		if u.Item != id || u.Snapshot == nil || u.Snapshot.State == "needs-human" {
			continue
		}
		if u.Snapshot.State == "running" || u.Snapshot.State == "in-review" {
			return u.Snapshot.State
		}
		return ""
	}
	return ""
}

// Authenticate checks a configured operator credential or an active execution credential.
func (s Service) Authenticate(token, operatorToken string) (Actor, error) {
	if token == "" {
		return Actor{}, problem(401, "credential required")
	}
	if sameSecret(token, operatorToken) && operatorToken != "" {
		return Actor{Name: "operator", Role: "operator"}, nil
	}
	st, err := s.Store.Snapshot()
	if err != nil {
		return Actor{}, err
	}
	a, ok := st.Auth[digest([]byte(token))]
	if !ok {
		return Actor{}, problem(401, "invalid credential")
	}
	at := st.Attempts[a.Attempt]
	if at.State == "ended" || st.Items[at.Item].Attempt != at.ID {
		return Actor{}, problem(403, "execution credential has ended")
	}
	return a, nil
}

// Dispatch recovers the same attempt and credential for a retried dispatch request.
func (s Service) Dispatch(item Item, host, harness, key string) (Response, error) {
	return s.Execute(Actor{Name: "coordinator", Role: "coordinator"}, Request{Op: "claim", ID: item.ID, Version: item.Version, Body: host, State: harness, Key: key})
}

// PendingQuestion includes a task's own questions and its parent plan's questions.
func PendingQuestion(st *State, id string) int {
	parent := st.Items[id].Plan
	for _, u := range st.Updates {
		if u.RequiresReply && (u.Item == id || parent != "" && u.Item == parent) && !resolved(st, u.ID) {
			return u.ID
		}
	}
	return 0
}

// Recovery marks an execution ended only after the caller has checked its host.
func (s Service) Recovery(id, evidence string) error {
	if evidence == "" {
		return fmt.Errorf("reconciliation evidence is required")
	}
	return s.SetAttempt(id, "ended", "")
}

// ReceiptsForDispatch supplies the persisted credential to an interrupted dispatch.
// It is server-side only and is never part of a snapshot API response.
func (s State) ReceiptsForDispatch(id string) []string {
	var tokens []string
	for _, receipt := range s.Receipts {
		if receipt.Response.Attempt != nil && receipt.Response.Attempt.ID == id && receipt.Response.Credential != "" {
			tokens = append(tokens, receipt.Response.Credential)
		}
	}
	return tokens
}
