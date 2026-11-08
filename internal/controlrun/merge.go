package controlrun

import (
	"errors"
	"fmt"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

// reportWindow is how long a merged task's session keeps its window, while
// waiting, to report before cleanup releases it.
const reportWindow = 10 * time.Minute

// merges answers each pending merge request under root. The merge runs on
// the task's host with that host's gh, as code: no agent's permission
// classifier judges it. A task waiting for the operator is left alone.
func (c *Coordinator) merges(root string, cfg *hosts.Config) error {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	if !st.Items[root].Coordinating {
		return nil
	}
	for _, t := range featureItems(st, root) {
		req, ok := control.PendingMerge(&st, t.ID)
		if !ok {
			continue
		}
		if err := c.merge(t.ID, req, cfg); err != nil {
			c.log("merge %s: %v", t.ID, err)
		}
	}
	return nil
}

// merge rereads the server just before the host call, so a question asked
// or a lock released since the pass began holds the merge.
func (c *Coordinator) merge(id string, req control.Update, cfg *hosts.Config) error {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	t := st.Items[id]
	if current, ok := control.PendingMerge(&st, id); !ok || current.ID != req.ID || t.State == "needs-human" || control.PendingQuestion(&st, id) > 0 {
		return nil
	}
	failures, held := control.MergeFailures(&st, req.ID)
	if held {
		return nil
	}
	at := st.Attempts[t.Attempt]
	if lock, ok := st.Locks[t.Repo]; !ok || lock.Attempt != at.ID {
		return c.answer(req, control.MergeRefused, "The task does not hold the repository merge lock; take it, refresh the default branch, gate and request the merge again.")
	}
	if t.State != "in-review" {
		return c.answer(req, control.MergeRefused, "The task is "+t.State+", not in-review; set it in-review and request the merge again.")
	}
	m := req.Merge
	host, ok := cfg.Hosts[at.Host]
	if !ok {
		return c.mergeFailed(req, failures, fmt.Sprintf("Cannot merge %s: host %s is not in hosts.toml.", m.PR, at.Host))
	}
	got, err := remote.ControlMerge(c.Client(host), hostops.MergeRequest{Repo: st.Items[t.Repo].Slug, PR: control.PRNumber(m.PR), Head: m.Head, Method: m.Method})
	var unreachable *remote.UnreachableError
	switch {
	case errors.As(err, &unreachable):
		return err
	case err != nil:
		return c.mergeFailed(req, failures, fmt.Sprintf(
			"Merging %s at %s failed on host %s: %v. Check gh auth status and engine setup there; acknowledge this message to retry.", m.PR, m.Head, at.Host, err))
	case got.Outcome == hostops.MergeDone:
		c.log("merged %s for %s: %s", m.PR, t.ID, got.Reason)
		return c.answer(req, control.MergeMerged, fmt.Sprintf("Merged %s (%s): %s.", m.PR, m.Method, got.Reason))
	case got.Outcome == hostops.MergeRefused:
		return c.answer(req, control.MergeRefused, "Not merged: "+got.Reason+". Fix it and request the merge again.")
	}
	c.log("merge %s waits: %s", t.ID, got.Reason)
	return nil
}

func (c *Coordinator) answer(req control.Update, outcome, body string) error {
	_, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{
		Op: "merge-outcome", ReplyTo: req.ID, State: outcome, Body: body, Key: fmt.Sprintf("merge/%d/%s", req.ID, outcome)})
	return err
}

// mergeFailed tells the operator, as a reply to the request, and holds the
// request until they acknowledge it.
func (c *Coordinator) mergeFailed(req control.Update, failures int, body string) error {
	_, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{
		Op: "say", ID: req.Item, ReplyTo: req.ID, Recipient: "operator", Body: body, Key: fmt.Sprintf("merge-failed/%d/%d", req.ID, failures)})
	return err
}

// justMerged keeps a merged task's session for its report.
func (c *Coordinator) justMerged(st control.State, id string) bool {
	at := control.MergedAt(&st, id)
	return !at.IsZero() && c.now().Sub(at) < reportWindow
}
