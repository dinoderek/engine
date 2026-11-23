package control

import (
	"cmp"
	"slices"
)

// overviewInbox bounds the inbox entries an overview carries. A full count
// means more may follow, which the inbox read pages through.
const overviewInbox = 200

// QuestionStatus is a question's standing as of the read: open until a
// resolution is recorded, which an acknowledgement or a reply does not do.
type QuestionStatus struct {
	ID           int    `json:"id"`
	State        string `json:"state"` // open or resolved
	Acknowledged bool   `json:"acknowledged,omitempty"`
	Replies      int    `json:"replies,omitempty"`
	// Resolution is the update that resolved it.
	Resolution int `json:"resolution,omitempty"`
}

// overview is what the dashboard's lists need in one read transaction: the
// operator's inbox (Updates, at most overviewInbox of them), every repository
// and plan with its status, the tasks the inbox is about (Items), and the
// session rows (Sessions).
func (s Service) overview(tx *Tx, a Actor) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	out := Response{}
	for u := range tx.InboxAfter(0, "") {
		out.Updates = append(out.Updates, u)
		if len(out.Updates) == overviewInbox {
			break
		}
	}
	for _, kind := range []string{"repo", "plan"} {
		for _, it := range tx.ItemsByKindUnder(kind, "") {
			out.Items = append(out.Items, withStatus(tx, it))
		}
	}
	shown := map[string]bool{}
	for _, it := range out.Items {
		shown[it.ID] = true
	}
	for _, u := range out.Updates {
		for _, id := range []string{u.Item, u.Task} {
			if it, ok := tx.Item(id); ok && !shown[id] {
				shown[id] = true
				out.Items = append(out.Items, it)
			}
		}
	}
	slices.SortFunc(out.Items, func(x, y Item) int { return Number(x.ID) - Number(y.ID) })
	out.Sessions = s.sessionRows(tx, tx.SessionAttempts())
	return out, nil
}

// planDetail is what the dashboard's plan pane needs in one read
// transaction: the plan view (Items), the current attempt of the plan and of
// each of its tasks (Attempts), and those items' session rows (Sessions).
func (s Service) planDetail(tx *Tx, a Actor, id string) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	out, err := planView(tx, a, id)
	if err != nil {
		return Response{}, err
	}
	out.Attempts = tx.CurrentAttemptsOfPlan(id)
	oldestFirst(out.Attempts)
	out.Sessions = s.sessionRows(tx, out.Attempts)
	return out, nil
}

// oldestFirst orders attempts by start; those started in the same instant by
// their number.
func oldestFirst(attempts []Attempt) {
	slices.SortFunc(attempts, func(a, b Attempt) int { return cmp.Or(a.Started.Compare(b.Started), Number(a.ID)-Number(b.ID)) })
}
