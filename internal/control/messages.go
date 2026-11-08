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
	kind := r.Op
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
		if err := pauseForQuestion(st, a, r.ID); err != nil {
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
	out := Response{Updates: []Update{u}}
	if kind == "resolved" && r.Item.Plan != "" && r.ID != r.Item.Plan {
		ruling := st.update(a, Request{ID: r.Item.Plan, ReplyTo: r.ReplyTo}, "ruling", r.Body, s.now())
		out.Updates = append(out.Updates, ruling)
	}
	return out, nil
}

// planRuling checks that a resolution published to a plan answers a
// question asked on that plan or on one of its tasks.
func planRuling(st *State, question Update, plan string) error {
	if st.Items[plan].Kind != "plan" {
		return problem(404, "unknown plan %s", plan)
	}
	if question.Item != plan && st.Items[question.Item].Plan != plan {
		return problem(400, "the question is not in plan %s", plan)
	}
	return nil
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
	if r.Item.Plan != "" && r.Op != "resolve" {
		return problem(400, "only resolve publishes a ruling to a plan")
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
		if r.Item.Plan != "" {
			if err := planRuling(st, parent, r.Item.Plan); err != nil {
				return r, "", err
			}
		}
		kind = "resolved"
	}
	return r, kind, nil
}

func pauseForQuestion(st *State, a Actor, id string) error {
	if id == "" {
		return nil
	}
	if !workerItem(st, a, id) {
		return problem(403, "cannot stop another execution")
	}
	item := st.Items[id]
	if item.Kind != "task" {
		return nil
	}
	if item.State != "running" && item.State != "in-review" && item.State != "needs-human" {
		return problem(409, "task is not active")
	}
	item.State = "needs-human"
	item.Version++
	st.Items[id] = item
	return nil
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
