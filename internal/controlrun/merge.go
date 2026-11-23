package controlrun

import (
	"errors"
	"slices"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
)

// queues runs every repository's merge queue, before any plan's pass, so a
// merged task is done for the rest of the pass. Each repository's steps are
// applied until none is left, then its host calls are made. A merge runs on
// the task's host with that host's gh, as code: no agent's permission
// classifier judges it.
func (c *Coordinator) queues(cfg *hosts.Config) error {
	var repos []string
	err := read(c.Service.Store, func(tx *control.Tx) error {
		for _, e := range tx.QueueEntries() {
			if !slices.Contains(repos, e.Repo) {
				repos = append(repos, e.Repo)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortFunc(repos, func(a, b string) int { return control.Number(a) - control.Number(b) })
	for _, repo := range repos {
		if err := c.queue(repo, cfg); err != nil {
			c.log("merge queue %s: %v", repo, err)
		}
	}
	return nil
}

// queue settles repo's queue's steps, then makes the head's host call. Each
// step can at most promote one head, unhead one entry or end one, so the
// loop ends within two rounds per entry.
func (c *Coordinator) queue(repo string, cfg *hosts.Config) error {
	for range 2*c.queued() + 2 {
		v, err := c.queueView(repo)
		if err != nil {
			return err
		}
		acts := reconcile.DecideQueue(v)
		stepped, err := c.steps(acts)
		if err != nil {
			return err
		}
		if stepped {
			continue
		}
		for _, a := range acts {
			if err := c.call(repo, a, cfg); err != nil {
				c.log("merge %s: %v", a.Item, err)
			}
		}
		return nil
	}
	return nil
}

// queued is how many entries the merge queues hold, 0 when the store cannot
// be read.
func (c *Coordinator) queued() (n int) {
	_ = read(c.Service.Store, func(tx *control.Tx) error { n = len(tx.QueueEntries()); return nil })
	return n
}

// slug is the owner/name of repository item id, empty when there is none.
func (c *Coordinator) slug(id string) (slug string) {
	_ = read(c.Service.Store, func(tx *control.Tx) error { repo, _ := tx.Item(id); slug = repo.Slug; return nil })
	return slug
}

// steps applies the queue steps among acts, and reports whether any did.
func (c *Coordinator) steps(acts []reconcile.Action) (bool, error) {
	stepped := false
	for _, a := range acts {
		if a.Kind != reconcile.QueueStep {
			continue
		}
		applied, err := c.Service.QueueStep(*a.Step)
		if err != nil {
			return stepped, err
		}
		stepped = stepped || applied
	}
	return stepped, nil
}

// call makes one host call of the queue, as reconcile decides on a reread
// just before it, so a question asked, a request made or a task withdrawn
// since the pass began holds or changes it. The reply is answered on what
// the server holds after the call.
func (c *Coordinator) call(repo string, a reconcile.Action, cfg *hosts.Config) error {
	v, err := c.queueView(repo)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(reconcile.DecideQueue(v), func(b reconcile.Action) bool {
		return b.Kind == reconcile.Merge && b.Entry == a.Entry
	}) {
		return nil
	}
	p := c.probe(c.slug(a.Entry.Repo), a, cfg)
	if v, err = c.queueView(repo); err != nil {
		return err
	}
	i := slices.IndexFunc(v.Entries, func(q reconcile.QueueItem) bool { return q.Task == a.Item })
	if i < 0 || v.Entries[i].QueueEntry != a.Entry {
		// A merge that landed while the entry changed is still recorded.
		if p.Reply.Code == hostops.CodeMerged {
			return c.Service.RecordMerged(a.Item, a.Entry.PR, "Merged "+a.Entry.PR+": "+p.Reply.Reason, a.Entry.Request)
		}
		return nil
	}
	if p.Reply.Code == hostops.CodeMerged {
		c.log("merged %s for %s: %s", a.Entry.PR, a.Item, p.Reply.Reason)
	}
	_, err = c.steps(reconcile.Answer(c.now(), v.Entries[i], p))
	return err
}

// probe makes the host call: a failure or an unreachable host is what the
// queue reads, never an error of its own.
func (c *Coordinator) probe(slug string, a reconcile.Action, cfg *hosts.Config) reconcile.Probe {
	host, ok := cfg.Hosts[a.Host]
	if !ok {
		return reconcile.Probe{Missing: true}
	}
	method := "squash"
	if a.Request.Merge != nil {
		method = a.Request.Merge.Method
	}
	got, err := remote.ControlMerge(c.Client(host), hostops.MergeRequest{Repo: slug,
		PR: control.PRNumber(a.Entry.PR), Head: a.Entry.Head, Method: method})
	var unreachable *remote.UnreachableError
	switch {
	case errors.As(err, &unreachable):
		return reconcile.Probe{Unreachable: true}
	case err != nil:
		return reconcile.Probe{Err: err.Error()}
	}
	return reconcile.Probe{Reply: got}
}

// queueView is repo's merge queue with the records it is decided on.
func (c *Coordinator) queueView(repo string) (reconcile.QueueView, error) {
	v := reconcile.QueueView{Now: c.now()}
	err := read(c.Service.Store, func(tx *control.Tx) error {
		for _, e := range tx.QueueOf(repo) {
			item, _ := tx.Item(e.Task)
			at, _ := tx.Attempt(item.Attempt)
			q := reconcile.QueueItem{QueueEntry: e, State: item.State, Question: control.OwnQuestion(tx, item.ID),
				Stopped: !coordinating(tx, item), Host: at.Host}
			if u, ok := control.PendingMerge(tx, item.ID); ok {
				q.Request = u
			}
			v.Entries = append(v.Entries, q)
		}
		return nil
	})
	return v, err
}
