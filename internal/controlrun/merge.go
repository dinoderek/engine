package controlrun

import (
	"errors"
	"fmt"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
)

// merge answers a merge request, as reconcile decides on a reread of the
// server just before the host call, so a question asked or a lock released
// since the pass began holds or refuses it. The merge runs on the task's
// host with that host's gh, as code: no agent's permission classifier
// judges it.
func (c *Coordinator) merge(a reconcile.Action, cfg *hosts.Config) error {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	t := st.Items[a.Item]
	again, ok := reconcile.MergeStep(record(st, t))
	if !ok || again.Request.ID != a.Request.ID {
		return nil
	}
	req := again.Request
	if again.Refuse != "" {
		return c.answer(req, control.MergeRefused, again.Refuse)
	}
	failures, _ := control.MergeFailures(&st, req.ID)
	at := st.Attempts[t.Attempt]
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
