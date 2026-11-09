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

// The updates the merge queue writes on a task. Each one wakes its
// executor; merged and merge-refused answer the request and end its entry.
const (
	MergeMerged    = "merged"
	MergeRefused   = "merge-refused"
	MergeTurn      = "merge-turn"
	MergeDemoted   = "merge-demoted"
	MergeWithdrawn = "merge-withdrawn"
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

// mergeRequest records a worker's merge request for its own task and puts
// the task in its repository's merge queue. A task already queued keeps its
// place with the new head, and a head told to refresh is checked again.
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
	m := *r.Merge
	u := st.update(a, Request{ID: item.ID}, "merge-request",
		fmt.Sprintf("Ready to merge %s at %s (%s); review %s.", m.PR, m.Head, m.Method, m.Verdict), s.now())
	u.Merge = &m
	st.Updates[len(st.Updates)-1] = u
	e, queued := st.Queue[item.ID]
	if !queued {
		e = QueueEntry{Task: item.ID, Repo: item.Repo, Seq: u.ID, Phase: QueueWaiting}
	}
	e.Request, e.PR, e.Head, e.WaitSince, e.Reason = u.ID, m.PR, m.Head, time.Time{}, ""
	if e.Phase == QueueRefresh {
		e.Phase, e.TurnAt = QueueCheck, time.Time{}
	}
	st.Queue[item.ID] = e
	return Response{Updates: []Update{u}, Queue: []QueueEntry{e}}, nil
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

// PendingMerge is the newest merge request of a task in the merge queue.
func PendingMerge(st *State, id string) (Update, bool) {
	e, ok := st.Queue[id]
	if !ok || e.Request < 1 || e.Request > len(st.Updates) {
		return Update{}, false
	}
	return st.Updates[e.Request-1], true
}

// withdraw takes item out of the merge queue, answering its request, when
// its task leaves in-review for anything but a hard stop.
func withdraw(st *State, a Actor, item Item, why string, now time.Time) {
	e, ok := st.Queue[item.ID]
	if !ok {
		return
	}
	delete(st.Queue, item.ID)
	st.update(a, Request{ID: item.ID, ReplyTo: e.Request}, MergeWithdrawn,
		"Left the merge queue: "+why+". Request the merge again once the PR is in-review, reviewed and pushed.", now)
}

// QueueStep is one change the coordinator makes to a merge queue entry. It
// applies only while the entry is still From, so a request, withdrawal or
// question that arrived since the pass read it wins.
type QueueStep struct {
	From QueueEntry
	// To replaces the entry; nil ends it.
	To *QueueEntry
	// Kind and Text, when Kind is set, are the update on the task that
	// wakes its executor, as a reply to the request.
	Kind, Text string
	// Notice, when set, tells the operator; Hold keeps the entry out of the
	// head until they acknowledge it.
	Notice string
	Hold   bool
	// Demote sends the entry to the back of the queue: its Seq becomes the
	// update's sequence. It needs Kind.
	Demote bool
	// Done records the task done with the entry's PR.
	Done bool
}

// QueueStep applies q in one transaction, and reports whether the entry was
// still q.From.
func (s Service) QueueStep(q QueueStep) (bool, error) {
	applied := false
	err := s.Store.Transaction(func(st *State) error {
		cur, ok := st.Queue[q.From.Task]
		if !ok || fingerprint(cur) != fingerprint(q.From) {
			return nil
		}
		applied = true
		a, now := Actor{Name: "coordinator", Role: "coordinator"}, s.now()
		var next *QueueEntry
		if q.To != nil {
			copied := *q.To
			next = &copied
		}
		if q.Done {
			item := st.Items[cur.Task]
			item.State, item.PR = "done", cur.PR
			item.Version++
			st.Items[item.ID] = item
		}
		if q.Kind != "" {
			u := st.update(a, Request{ID: cur.Task, ReplyTo: cur.Request}, q.Kind, q.Text, now)
			if q.Demote && next != nil {
				next.Seq = u.ID
			}
		}
		if q.Notice != "" {
			n := st.update(a, Request{ID: cur.Task, ReplyTo: cur.Request, Recipient: "operator"}, "say", q.Notice, now)
			if q.Hold && next != nil {
				next.Held = n.ID
			}
		}
		if next == nil {
			delete(st.Queue, cur.Task)
		} else {
			st.Queue[cur.Task] = *next
		}
		return nil
	})
	return applied, err
}

// QueueHeld reports whether e waits for the operator to acknowledge the
// notice that held it.
func QueueHeld(st *State, e QueueEntry) bool {
	return e.Held > 0 && !acknowledged(st, e.Held)
}

// Queue is repo's merge queue in order.
func Queue(st *State, repo string) []QueueEntry {
	var out []QueueEntry
	for _, e := range st.Queue {
		if e.Repo == repo {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b QueueEntry) int { return a.Seq - b.Seq })
	return out
}

func mergeQueue(st *State, a Actor, repo string) (Response, error) {
	if st.Items[repo].Kind != "repo" {
		return Response{}, problem(404, "unknown repository %s", repo)
	}
	if !readable(st, a, repo) {
		return Response{}, problem(403, "repository is outside this execution's read scope")
	}
	return Response{Queue: Queue(st, repo)}, nil
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
