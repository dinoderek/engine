package control

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MergeRequest is an executor's signal that its PR is ready for the
// coordinator to merge: the PR, the head commit that was reviewed and gated,
// the repository's merge method and the reviewer's verdict.
type MergeRequest struct {
	PR      string `json:"pr"`
	Head    string `json:"head"`
	Method  string `json:"method"`
	Verdict string `json:"verdict"`
}

// The outcomes the coordinator records for a merge request.
const (
	MergeMerged  = "merged"
	MergeRefused = "merge-refused"
)

var headRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// PRNumber is the number in a GitHub PR URL; 0 when there is none.
func PRNumber(url string) int {
	i := strings.LastIndex(url, "/pull/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(url[i+len("/pull/"):])
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// mergeRequest records a worker's merge request for its own task. It holds
// the repository merge lock, so no sibling moves the default branch before
// the coordinator merges.
func (s Service) mergeRequest(st *State, a Actor, r Request) (Response, error) {
	if a.Role != "worker" {
		return Response{}, problem(403, "only the task's execution can request its merge")
	}
	item, ok := st.Items[r.ID]
	if !ok || item.Kind != "task" || item.Attempt != a.Attempt {
		return Response{}, problem(403, "only the task's execution can request its merge")
	}
	if err := validMerge(r.Merge, st.Items[item.Repo].Slug); err != nil {
		return Response{}, err
	}
	if item.State != "in-review" {
		return Response{}, problem(409, "a merge request needs the task in-review, not %s", item.State)
	}
	if item.PR != r.Merge.PR {
		return Response{}, problem(409, "the merge request's PR is not the task's; record it with the in-review state")
	}
	if PendingQuestion(st, item.ID) > 0 {
		return Response{}, problem(409, "operator question is unresolved")
	}
	lock, held := st.Locks[item.Repo]
	if !held || lock.Attempt != a.Attempt || lock.Token != r.Token {
		return Response{}, problem(409, "take the repository merge lock before requesting the merge")
	}
	m := *r.Merge
	u := st.update(a, Request{ID: item.ID}, "merge-request",
		fmt.Sprintf("Ready to merge %s at %s (%s); review %s.", m.PR, m.Head, m.Method, m.Verdict), s.now())
	u.Merge = &m
	st.Updates[len(st.Updates)-1] = u
	return Response{Updates: []Update{u}}, nil
}

func validMerge(m *MergeRequest, slug string) error {
	switch {
	case m == nil:
		return problem(400, "a merge request needs --pr, --head, --method and --verdict")
	case !completionPR(m.PR, slug):
		return problem(400, "a GitHub PR URL for this repository is required")
	case !headRe.MatchString(m.Head):
		return problem(400, "--head must be the full 40-character commit that was reviewed and gated")
	case !slices.Contains([]string{"squash", "merge", "rebase"}, m.Method):
		return problem(400, "--method must be squash, merge or rebase")
	case m.Verdict != "APPROVED":
		return problem(409, "the merge needs the fresh reviewer's APPROVED verdict")
	}
	return nil
}

// PendingMerge is the task's newest merge request from its current execution
// that the coordinator has not answered. A newer request supersedes an older
// one.
func PendingMerge(st *State, id string) (Update, bool) {
	item := st.Items[id]
	for i := len(st.Updates) - 1; i >= 0; i-- {
		u := st.Updates[i]
		if u.Item != id || u.Kind != "merge-request" || u.Merge == nil {
			continue
		}
		if u.Author != item.Attempt || mergeAnswered(st, u.ID) {
			return Update{}, false
		}
		return u, true
	}
	return Update{}, false
}

func mergeAnswered(st *State, id int) bool {
	for _, u := range st.Updates[id:] {
		if u.ReplyTo == id && (u.Kind == MergeMerged || u.Kind == MergeRefused) {
			return true
		}
	}
	return false
}

// mergeOutcome records the coordinator's answer to a merge request. A merged
// PR completes the task, records the PR and frees the repository lock in the
// same transaction. A refusal hands the task back to its executor.
func (s Service) mergeOutcome(st *State, a Actor, r Request) (Response, error) {
	if a.Role != "coordinator" {
		return Response{}, problem(403, "only the coordinator records a merge")
	}
	if r.ReplyTo < 1 || r.ReplyTo > len(st.Updates) {
		return Response{}, problem(404, "unknown merge request")
	}
	req := st.Updates[r.ReplyTo-1]
	item, ok := st.Items[req.Item]
	if !ok || req.Kind != "merge-request" || req.Merge == nil {
		return Response{}, problem(404, "unknown merge request")
	}
	if mergeAnswered(st, req.ID) {
		return Response{}, problem(409, "merge request was already answered")
	}
	if r.State != MergeMerged && r.State != MergeRefused || r.Body == "" {
		return Response{}, problem(400, "a merge outcome is merged or merge-refused, with a reason")
	}
	if r.State == MergeMerged {
		item.State, item.PR = "done", req.Merge.PR
		item.Version++
		st.Items[item.ID] = item
		if lock, held := st.Locks[item.Repo]; held && lock.Attempt == req.Author {
			delete(st.Locks, item.Repo)
			st.update(a, Request{ID: item.ID}, "lock-released", lock.Attempt, s.now())
		}
	}
	u := st.update(a, Request{ID: item.ID, ReplyTo: req.ID}, r.State, r.Body, s.now())
	return Response{Item: &item, Updates: []Update{u}}, nil
}

// MergedAt is when the coordinator recorded the task's merge; zero when it
// has not.
func MergedAt(st *State, id string) time.Time {
	for i := len(st.Updates) - 1; i >= 0; i-- {
		if u := st.Updates[i]; u.Item == id && u.Kind == MergeMerged {
			return u.At
		}
	}
	return time.Time{}
}

// MergeFailures counts the coordinator's failure notices for a merge
// request, and reports whether the newest is still unacknowledged, which
// holds the request until the operator has seen it.
func MergeFailures(st *State, request int) (count int, held bool) {
	for _, u := range st.Updates[min(request, len(st.Updates)):] {
		if u.ReplyTo == request && u.Kind == "say" && u.Author == "coordinator" && u.Recipient == "operator" {
			count++
			held = !acknowledged(st, u.ID)
		}
	}
	return count, held
}
