package control

import "reflect"

// ackAll acknowledges, in one transaction, every informational message the
// operator inbox lists: one acknowledgement each, as ack writes. It never
// touches a question. r.Item.Repo limits it to one repository's items, and
// a nonzero r.Before to messages numbered below it.
func (s Service) ackAll(tx *Tx, a Actor, r Request) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	// An acknowledgement cannot be undone, so a scope ack-all does not
	// know, such as a plan, is refused rather than widened to everything.
	scope := Request{Op: r.Op, Key: r.Key, Attempt: r.Attempt, Item: Item{Repo: r.Item.Repo}, Before: r.Before}
	if !reflect.DeepEqual(r, scope) {
		return Response{}, problem(400, "ack-all takes only a repository and an upper bound")
	}
	if r.Before < 0 {
		return Response{}, problem(400, "before is an update sequence")
	}
	if repo := r.Item.Repo; repo != "" {
		if item, ok := tx.Item(repo); !ok || item.Kind != "repo" {
			return Response{}, problem(404, "unknown repository")
		}
	}
	var pending []Update
	for u := range tx.NoticesBefore(r.Before) {
		if r.Item.Repo != "" && updateRepo(tx, u) != r.Item.Repo {
			continue
		}
		pending = append(pending, u)
	}
	out := Response{Updates: []Update{}}
	for _, u := range pending {
		out.Updates = append(out.Updates, tx.AppendUpdate(a, Request{ID: u.Item, ReplyTo: u.ID}, "acknowledged", "", s.now()))
	}
	return out, nil
}

// updateRepo is the repository an update's item belongs to, or "" for an
// update about no item.
func updateRepo(tx *Tx, u Update) string {
	item, _ := tx.Item(u.Item)
	if item.Kind == "repo" {
		return item.ID
	}
	return item.Repo
}
