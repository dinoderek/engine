package control

import (
	"context"
	"fmt"
	"slices"
)

func (s Service) messages(tx *Tx, a Actor, r Request) (Response, error) {
	if r.Op == "updates" || r.Op == "inbox" {
		if r.Op == "inbox" && a.Role == "worker" {
			return Response{}, problem(403, "operator inbox requires operator access")
		}
		return messageListing(tx, a, r), nil
	}
	if err := messageTarget(tx, a, r); err != nil {
		return Response{}, err
	}
	kind, asker := r.Op, ""
	switch r.Op {
	case "ack", "resolve", "reply":
		var err error
		r, kind, err = replyCommand(tx, a, r)
		if err != nil {
			return Response{}, err
		}
	case "ask":
		r.Recipient = "operator"
		r.RequiresReply = true
		var err error
		if asker, err = pauseForQuestion(tx, a, r.ID); err != nil {
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
	u := tx.AppendUpdate(a, r, kind, r.Body, s.now())
	if asker != "" {
		u.Task = asker
		tx.PutUpdate(u)
	}
	out := Response{Updates: []Update{u}}
	if kind == "resolved" {
		question, _ := tx.Update(r.ReplyTo)
		out.Updates = append(out.Updates, s.release(tx, a, question)...)
	}
	return out, nil
}

// messageTarget checks the item a message is about and where it may publish.
func messageTarget(tx *Tx, a Actor, r Request) error {
	if r.ID != "" {
		target, ok := tx.Item(r.ID)
		if !ok {
			return problem(404, "unknown item")
		}
		if a.Role == "worker" && target.Repo != executionOwner(tx, a).Repo {
			return problem(403, "cannot message another repository")
		}
	}
	if r.Item.Plan != "" {
		return problem(400, "a message takes no plan; ask on the plan itself")
	}
	return nil
}

func messageListing(tx *Tx, a Actor, r Request) Response {
	out := Response{}
	updates := tx.UpdatesAfter(r.After, r.ID)
	if r.Op == "inbox" {
		updates = tx.InboxAfter(r.After, r.ID)
	}
	for u := range updates {
		if !readableUpdate(tx, a, u) {
			continue
		}
		out.Updates = append(out.Updates, u)
		if len(out.Updates) == 200 {
			break
		}
	}
	return out
}

func replyCommand(tx *Tx, a Actor, r Request) (Request, string, error) {
	parent, ok := tx.Update(r.ReplyTo)
	if !ok {
		return r, "", problem(404, "unknown update")
	}
	if parent.Recipient == "operator" {
		if err := operator(a); err != nil {
			return r, "", err
		}
	}
	if parent.Item != "" && !workerItem(tx.LegacyState(), a, parent.Item) {
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
		if err := alreadyResolved(tx, r.ReplyTo); err != nil {
			return r, "", err
		}
		kind = "resolved"
	}
	return r, kind, nil
}

// pauseForQuestion holds the task a question stops in needs-human: the task
// asked on, or the asking task when a task's session asks on its own plan.
// It returns that asking task, which the plan question records.
func pauseForQuestion(tx *Tx, a Actor, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	held, asker := id, ""
	owner := executionOwner(tx, a)
	if a.Role == "worker" && owner.Kind == "task" && owner.Plan == id {
		held, asker = owner.ID, owner.ID
	} else if !workerItem(tx.LegacyState(), a, id) {
		return "", problem(403, "cannot stop another execution")
	}
	item, _ := tx.Item(held)
	if item.Kind != "task" {
		return "", nil
	}
	if item.State != "running" && item.State != "in-review" && item.State != "needs-human" {
		return "", problem(409, "task is not active")
	}
	item.State = "needs-human"
	item.Version++
	tx.PutItem(item)
	return asker, nil
}

// release restores each task a resolved question held to the state it had
// before needs-human, in the resolution's transaction: the question's task
// or asker, and, for a plan question, every task in the plan. It leaves a
// task alone while another question holds it, once its state changed
// meanwhile, when its execution ended (the coordinator then resumes it), or
// when no question held it.
func (s Service) release(tx *Tx, a Actor, question Update) []Update {
	ids := []string{question.Task, question.Item}
	if qi, _ := tx.Item(question.Item); qi.Kind == "plan" {
		for _, t := range tx.ItemsOfPlan(question.Item) {
			ids = append(ids, t.ID)
		}
	}
	slices.SortFunc(ids, func(x, y string) int { return Number(x) - Number(y) })
	var out []Update
	for _, id := range slices.Compact(ids) {
		item, ok := tx.Item(id)
		if !ok || item.Kind != "task" || item.State != "needs-human" || !active(tx.LegacyState(), item) || OwnQuestion(tx, id) > 0 {
			continue
		}
		prior := heldFrom(tx, id)
		if prior == "" {
			continue
		}
		item.State = prior
		item.Version++
		tx.PutItem(item)
		out = append(out, tx.AppendUpdate(a, Request{ID: id}, "state", fmt.Sprintf("%s (version %d)", item.State, item.Version), s.now()))
	}
	return out
}

// heldFrom is the state a task held by a question had before needs-human:
// the state in its newest update snapshot that is not needs-human, provided
// a question on it, or one it asked, came after that snapshot.
func heldFrom(tx *Tx, id string) string {
	asked := false
	for u := range tx.UpdatesAboutNewestFirst(id) {
		if u.RequiresReply {
			asked = true
		}
		if u.Item != id || u.Snapshot == nil || u.Snapshot.State == "needs-human" {
			continue
		}
		if asked && (u.Snapshot.State == "running" || u.Snapshot.State == "in-review") {
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
	var actor Actor
	err := s.Store.Read(context.Background(), func(tx *Tx) error {
		a, ok := tx.Credential(digest([]byte(token)))
		if !ok {
			return problem(401, "invalid credential")
		}
		at, _ := tx.Attempt(a.Attempt)
		if item, _ := tx.Item(at.Item); at.State == "ended" || item.Attempt != at.ID {
			return problem(403, "execution credential has ended")
		}
		actor = a
		return nil
	})
	return actor, err
}

// Dispatch recovers the same attempt and credential for a retried dispatch request.
func (s Service) Dispatch(item Item, host, harness, key string) (Response, error) {
	return s.Execute(Actor{Name: "coordinator", Role: "coordinator"}, Request{Op: "claim", ID: item.ID, Version: item.Version, Body: host, State: harness, Key: key})
}

// OwnQuestion is the first open question that holds item id: one asked on
// it, or asked by its session on its plan. It is 0 when none is open. A
// plan's question holds the task that asked it, not the plan's other tasks.
func OwnQuestion(tx *Tx, id string) int {
	for u := range tx.QuestionsAbout(id) {
		if tx.Resolution(u.ID) == 0 {
			return u.ID
		}
	}
	return 0
}

// PlanQuestion is an open question on the plan of task id, or 0. It blocks
// only the first dispatch of a task in that plan.
func PlanQuestion(tx *Tx, id string) int {
	item, _ := tx.Item(id)
	if item.Plan == "" {
		return 0
	}
	return OwnQuestion(tx, item.Plan)
}

// Recovery marks an execution ended only after the caller has checked its host.
func (s Service) Recovery(id, evidence string) error {
	if evidence == "" {
		return fmt.Errorf("reconciliation evidence is required")
	}
	return s.SetAttempt(id, "ended", "")
}
