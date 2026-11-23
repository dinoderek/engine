package control

import (
	"context"
	"iter"
	"maps"
	"slices"
	"time"
)

// Tx is the typed access to the control records inside one Store.Read or
// Store.Write. Domain code reaches records only through its methods, each
// named by what it fetches and shaped as one indexed query, so a storage
// engine can answer it without loading anything else. A Tx is valid only
// inside the function it is given to.
type Tx struct {
	st    *state
	write bool
}

// Read runs fn on a consistent snapshot and commits nothing. Reads run beside
// each other and see every earlier commit whole or not at all.
func (s *Store) Read(ctx context.Context, fn func(*Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.view(func(st *state) error { return fn(&Tx{st: st}) })
}

// Write runs fn in the one writing transaction: every change it makes commits
// together, and an error from fn leaves the records as they were.
func (s *Store) Write(ctx context.Context, fn func(*Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.modify(func(st *state) error { return fn(&Tx{st: st, write: true}) })
}

// tx is a writable Tx on s, for the code and tests of this package that hold
// a state outside a Store.Read or Store.Write.
func (s *state) tx() *Tx { return &Tx{st: s, write: true} }

func (t *Tx) mustWrite() {
	if !t.write {
		panic("control: write in a read transaction")
	}
}

// Item is the item with this ID.
func (t *Tx) Item(id string) (Item, bool) {
	item, ok := t.st.Items[id]
	return item, ok
}

// item is the item with this ID, the zero Item when there is none.
func (t *Tx) item(id string) Item {
	item, _ := t.Item(id)
	return item
}

// attempt is the attempt with this ID, the zero Attempt when there is none.
func (t *Tx) attempt(id string) Attempt {
	at, _ := t.Attempt(id)
	return at
}

// update is the update with this sequence, the zero Update when there is none.
func (t *Tx) update(id int) Update {
	u, _ := t.Update(id)
	return u
}

// PutItem stores item under its ID.
func (t *Tx) PutItem(item Item) {
	t.mustWrite()
	t.st.Items[item.ID] = item
}

// DeleteItem removes the item with this ID.
func (t *Tx) DeleteItem(id string) {
	t.mustWrite()
	delete(t.st.Items, id)
}

// CoordinatedItems are the items whose coordination intent is set, in no
// order.
func (t *Tx) CoordinatedItems() []Item {
	var out []Item
	for _, item := range t.st.Items {
		if item.Coordinating {
			out = append(out, item)
		}
	}
	return out
}

// ItemsOfPlan are the tasks of plan, in no order.
func (t *Tx) ItemsOfPlan(plan string) []Item {
	var out []Item
	for _, item := range t.st.Items {
		if item.Plan == plan {
			out = append(out, item)
		}
	}
	return out
}

// ItemsByKindUnder are the items of a kind that belong to repo or plan
// parent, in no order. An empty kind or parent matches every one: the
// list operation relies on that scan, and an indexed query must not.
func (t *Tx) ItemsByKindUnder(kind, parent string) []Item {
	var out []Item
	for _, item := range t.st.Items {
		if kind != "" && item.Kind != kind || parent != "" && item.Repo != parent && item.Plan != parent {
			continue
		}
		out = append(out, item)
	}
	return out
}

// Attempt is the attempt with this ID.
func (t *Tx) Attempt(id string) (Attempt, bool) {
	at, ok := t.st.Attempts[id]
	return at, ok
}

// AttemptsFor are the attempts of item, in no order. An empty item matches
// every attempt: the attempts operation relies on that scan, and an indexed
// query must not.
func (t *Tx) AttemptsFor(item string) []Attempt {
	var out []Attempt
	for _, at := range t.st.Attempts {
		if item == "" || at.Item == item {
			out = append(out, at)
		}
	}
	return out
}

// CurrentAttempts are the attempts that are their item's current one, in no
// order, ended or not.
func (t *Tx) CurrentAttempts() []Attempt {
	var out []Attempt
	for _, at := range t.st.Attempts {
		if item, ok := t.st.Items[at.Item]; ok && item.Attempt == at.ID {
			out = append(out, at)
		}
	}
	return out
}

// QueueEntry is task's place in its repository's merge queue.
func (t *Tx) QueueEntry(task string) (QueueEntry, bool) {
	e, ok := t.st.Queue[task]
	return e, ok
}

// Credential is the actor an execution credential's digest names.
func (t *Tx) Credential(hash string) (Actor, bool) {
	a, ok := t.st.Auth[hash]
	return a, ok
}

// Receipt is the stored result of a request key's fingerprint.
func (t *Tx) Receipt(key string) (receipt, bool) {
	r, ok := t.st.Receipts[key]
	return r, ok
}

// PutReceipt stores the result of a request key's fingerprint.
func (t *Tx) PutReceipt(key string, r receipt) {
	t.mustWrite()
	t.st.Receipts[key] = r
}

// DispatchTokens are the credentials a dispatch of attempt id left in its
// receipts, for an interrupted dispatch to recover. They are server-side
// only and never part of an API response.
func (t *Tx) DispatchTokens(id string) []string {
	var tokens []string
	for _, receipt := range t.st.Receipts {
		if receipt.Response.Attempt != nil && receipt.Response.Attempt.ID == id && receipt.Response.Credential != "" {
			tokens = append(tokens, receipt.Response.Credential)
		}
	}
	return tokens
}

// Update is the update with this sequence.
func (t *Tx) Update(id int) (Update, bool) {
	if id < 1 || id > len(t.st.Updates) {
		return Update{}, false
	}
	return t.st.Updates[id-1], true
}

// AppendUpdate records the next update and returns it, with the item it is
// about as it stands.
func (t *Tx) AppendUpdate(a Actor, r Request, kind, body string, now time.Time) Update {
	t.mustWrite()
	return t.st.update(a, r, kind, body, now)
}

// PutUpdate replaces an update, which keeps its sequence.
func (t *Tx) PutUpdate(u Update) {
	t.mustWrite()
	t.st.Updates[u.ID-1] = u
}

// UpdatesAfter are the updates numbered above after, oldest first, about
// item, or about any when item is empty.
func (t *Tx) UpdatesAfter(after int, item string) iter.Seq[Update] {
	return func(yield func(Update) bool) {
		for _, u := range t.st.Updates {
			if u.ID <= after || item != "" && u.Item != item {
				continue
			}
			if !yield(u) {
				return
			}
		}
	}
}

// answers are the questions an update resolved and the updates an
// acknowledgement answered.
func (t *Tx) answers() (resolved, acked map[int]bool) {
	resolved, acked = map[int]bool{}, map[int]bool{}
	for _, u := range t.st.Updates {
		switch u.Kind {
		case "resolved", KindPaneAnswer:
			resolved[u.ReplyTo] = true
		case "acknowledged":
			acked[u.ReplyTo] = true
		}
	}
	return resolved, acked
}

// InboxAfter are the operator's inbox entries numbered above after, oldest
// first, about item or about any when item is empty: updates to the
// operator that no resolution answers, and, unless they ask for a reply,
// no acknowledgement either.
func (t *Tx) InboxAfter(after int, item string) iter.Seq[Update] {
	return func(yield func(Update) bool) {
		resolved, acked := t.answers()
		for u := range t.UpdatesAfter(after, item) {
			if u.Recipient != "operator" || resolved[u.ID] || !u.RequiresReply && acked[u.ID] {
				continue
			}
			if !yield(u) {
				return
			}
		}
	}
}

// NoticesBefore are the messages to the operator that need no reply and
// that neither a resolution nor an acknowledgement answered, oldest first,
// numbered below before; a before of 0 is no bound.
func (t *Tx) NoticesBefore(before int) iter.Seq[Update] {
	return func(yield func(Update) bool) {
		resolved, acked := t.answers()
		for _, u := range t.st.Updates {
			if before > 0 && u.ID >= before {
				return
			}
			if u.Recipient != "operator" || u.RequiresReply || acked[u.ID] || resolved[u.ID] {
				continue
			}
			if !yield(u) {
				return
			}
		}
	}
}

// UpdatesAboutNewestFirst are the updates about item, or about a question
// item asked on its plan, newest first.
func (t *Tx) UpdatesAboutNewestFirst(item string) iter.Seq[Update] {
	return func(yield func(Update) bool) {
		for i := len(t.st.Updates) - 1; i >= 0; i-- {
			if u := t.st.Updates[i]; u.Item == item || u.Task == item {
				if !yield(u) {
					return
				}
			}
		}
	}
}

// QuestionsAbout are the questions asked on item, or by item on its plan,
// oldest first, resolved or not.
func (t *Tx) QuestionsAbout(item string) iter.Seq[Update] {
	return func(yield func(Update) bool) {
		for _, u := range t.st.Updates {
			if u.RequiresReply && (u.Item == item || u.Task == item) {
				if !yield(u) {
					return
				}
			}
		}
	}
}

// Resolution is the update that resolved question id, by the operator or
// by their answer typed into the asking session's pane, or 0.
func (t *Tx) Resolution(id int) int {
	for _, u := range t.st.Updates {
		if u.ReplyTo == id && (u.Kind == "resolved" || u.Kind == KindPaneAnswer) {
			return u.ID
		}
	}
	return 0
}

// NextID is the next unused ID of an item or attempt, with prefix.
func (t *Tx) NextID(prefix string) string {
	t.mustWrite()
	return t.st.id(prefix)
}

// RepoBySlug is the repository item with this owner/name slug.
func (t *Tx) RepoBySlug(slug string) (Item, bool) {
	for _, item := range t.st.Items {
		if item.Kind == "repo" && item.Slug == slug {
			return item, true
		}
	}
	return Item{}, false
}

// PutAttempt stores at under its ID.
func (t *Tx) PutAttempt(at Attempt) {
	t.mustWrite()
	t.st.Attempts[at.ID] = at
}

// UnendedAttempts are the attempts not yet ended, in no order.
func (t *Tx) UnendedAttempts() []Attempt {
	var out []Attempt
	for _, at := range t.st.Attempts {
		if at.State != "ended" {
			out = append(out, at)
		}
	}
	return out
}

// AttemptsOnHost are the attempts that ran or run on host, in no order.
func (t *Tx) AttemptsOnHost(host string) []Attempt {
	var out []Attempt
	for _, at := range t.st.Attempts {
		if at.Host == host {
			out = append(out, at)
		}
	}
	return out
}

// HostedTaskAttempts are the attempts of tasks that name both their host and
// their harness, in no order.
func (t *Tx) HostedTaskAttempts() []Attempt {
	var out []Attempt
	for _, at := range t.st.Attempts {
		if at.Host != "" && at.Harness != "" && t.st.Items[at.Item].Kind == "task" {
			out = append(out, at)
		}
	}
	return out
}

// PutCredential stores the actor an execution credential's digest names.
func (t *Tx) PutCredential(hash string, a Actor) {
	t.mustWrite()
	t.st.Auth[hash] = a
}

// QueueOf is repo's merge queue in order.
func (t *Tx) QueueOf(repo string) []QueueEntry {
	var out []QueueEntry
	for _, e := range t.st.Queue {
		if e.Repo == repo {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b QueueEntry) int { return a.Seq - b.Seq })
	return out
}

// QueueEntries are the entries of every repository's merge queue, in no
// order.
func (t *Tx) QueueEntries() []QueueEntry {
	return slices.Collect(maps.Values(t.st.Queue))
}

// PutQueueEntry stores e under its task, in its repository's merge queue.
func (t *Tx) PutQueueEntry(e QueueEntry) {
	t.mustWrite()
	t.st.Queue[e.Task] = e
}

// DeleteQueueEntry takes task out of its repository's merge queue.
func (t *Tx) DeleteQueueEntry(task string) {
	t.mustWrite()
	delete(t.st.Queue, task)
}

// Host is what the coordinator saved of host; the zero record when nothing.
func (t *Tx) Host(name string) HostRecord { return t.st.Hosts[name] }

// PutHost saves rec as what the coordinator knows of host.
func (t *Tx) PutHost(name string, rec HostRecord) {
	t.mustWrite()
	t.st.Hosts[name] = rec
}

// HostRecords are the saved host records by name, as a copy.
func (t *Tx) HostRecords() map[string]HostRecord { return maps.Clone(t.st.Hosts) }

// PassAt is the last time the coordinator was known to run, zero before the
// first pass.
func (t *Tx) PassAt() time.Time { return t.st.PassAt }

// SetPassAt saves the last time the coordinator was known to run.
func (t *Tx) SetPassAt(at time.Time) {
	t.mustWrite()
	t.st.PassAt = at
}

// Upgrade is the newest `engine upgrade` run's start and end.
func (t *Tx) Upgrade() UpgradeRecord { return t.st.Upgrade }

// SetUpgrade replaces the newest `engine upgrade` run's record.
func (t *Tx) SetUpgrade(u UpgradeRecord) {
	t.mustWrite()
	t.st.Upgrade = u
}

// Authors are the names that wrote an update.
func (t *Tx) Authors() map[string]bool {
	out := map[string]bool{}
	for _, u := range t.st.Updates {
		out[u.Author] = true
	}
	return out
}

// LatestUpdate is the newest update of kind about item.
func (t *Tx) LatestUpdate(item, kind string) (Update, bool) {
	for i := len(t.st.Updates) - 1; i >= 0; i-- {
		if u := t.st.Updates[i]; u.Item == item && u.Kind == kind {
			return u, true
		}
	}
	return Update{}, false
}
