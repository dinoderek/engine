package control

import (
	"database/sql"
	"errors"
	"iter"
	"math"
	"time"
)

// pageSize is how many updates an iterator reads per query: a walk that
// stops early reads little, and a long one never holds its rows open.
const pageSize = 256

// answered is the condition that an update u has a reply of one of kinds.
func answered(kinds string) string {
	return "EXISTS (SELECT 1 FROM updates r WHERE r.reply_to=u.id AND r.kind IN (" + kinds + "))"
}

const (
	resolvedKinds = "'resolved','" + KindPaneAnswer + "'"
	ackedKinds    = "'acknowledged'"
)

// scan yields the updates query finds, page by page. query's first argument
// is the cursor, the ID it walks from, and its last the page size.
func (t *Tx) scan(query string, from int, args ...any) iter.Seq[Update] {
	return func(yield func(Update) bool) {
		cursor := from
		for {
			page := fetchAll[Update](t, query, append(append([]any{cursor}, args...), pageSize)...)
			for _, u := range page {
				cursor = u.ID
				if !yield(u) {
					return
				}
			}
			if len(page) < pageSize {
				return
			}
		}
	}
}

// Update is the update with this sequence.
func (t *Tx) Update(id int) (Update, bool) {
	return fetch[Update](t, "SELECT data FROM updates WHERE id=?", id)
}

// update is the update with this sequence, the zero Update when there is none.
func (t *Tx) update(id int) Update {
	u, _ := t.Update(id)
	return u
}

// AppendUpdate records the next update and returns it, with a snapshot of
// the item it is about as it stands (slim).
func (t *Tx) AppendUpdate(a Actor, r Request, kind, body string, now time.Time) Update {
	t.mustWrite()
	var last int
	if t.err == nil {
		t.fail(t.q.QueryRowContext(t.ctx, "SELECT COALESCE(MAX(id), 0) FROM updates").Scan(&last))
	}
	u := Update{ID: last + 1, Item: r.ID, Author: a.Name, At: now.UTC(), Kind: kind, Body: body,
		Recipient: r.Recipient, ReplyTo: r.ReplyTo, RequiresReply: r.RequiresReply,
		Choices: r.Choices, Recommended: r.Recommended, Class: r.Class}
	if item, ok := t.Item(r.ID); ok {
		u.Snapshot = slim(item)
	}
	t.PutUpdate(u)
	return u
}

// PutUpdate stores u under its sequence, replacing any update there.
func (t *Tx) PutUpdate(u Update) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO updates(id,item,kind,author,reply_to,recipient,task,requires_reply,data) VALUES(?,?,?,?,?,?,?,?,?)",
		u.ID, u.Item, u.Kind, u.Author, u.ReplyTo, u.Recipient, u.Task, u.RequiresReply, t.encode(u))
}

// UpdatesAfter are the updates numbered above after, oldest first, about
// item, or about any when item is empty.
func (t *Tx) UpdatesAfter(after int, item string) iter.Seq[Update] {
	if item == "" {
		return t.scan("SELECT data FROM updates WHERE id>? ORDER BY id LIMIT ?", after)
	}
	return t.scan("SELECT data FROM updates WHERE id>? AND item=? ORDER BY id LIMIT ?", after, item)
}

// InboxAfter are the operator's inbox entries numbered above after, oldest
// first, about item or about any when item is empty: updates to the
// operator that no resolution answers, and, unless they ask for a reply,
// no acknowledgement either.
func (t *Tx) InboxAfter(after int, item string) iter.Seq[Update] {
	about := ""
	if item != "" {
		about = " AND u.item=?2"
	}
	return t.scan("SELECT data FROM updates u WHERE u.id>?1 AND u.recipient='operator'"+about+
		" AND NOT "+answered(resolvedKinds)+" AND (u.requires_reply OR NOT "+answered(ackedKinds)+")"+
		" ORDER BY u.id LIMIT ?3", after, item)
}

// NoticesBefore are the messages to the operator that need no reply and
// that neither a resolution nor an acknowledgement answered, oldest first,
// numbered below before; a before of 0 is no bound.
func (t *Tx) NoticesBefore(before int) iter.Seq[Update] {
	if before <= 0 {
		before = math.MaxInt64
	}
	return t.scan("SELECT data FROM updates u WHERE u.id>? AND u.id<? AND u.recipient='operator' AND NOT u.requires_reply"+
		" AND NOT "+answered(resolvedKinds+","+ackedKinds)+" ORDER BY u.id LIMIT ?", 0, before)
}

// UpdatesAboutNewestFirst are the updates about item, or about a question
// item asked on its plan, newest first.
func (t *Tx) UpdatesAboutNewestFirst(item string) iter.Seq[Update] {
	return t.scan("SELECT data FROM updates WHERE id<? AND (item=?2 OR task=?2) ORDER BY id DESC LIMIT ?", math.MaxInt64, item)
}

// QuestionsAbout are the questions asked on item, or by item on its plan,
// oldest first, resolved or not.
func (t *Tx) QuestionsAbout(item string) iter.Seq[Update] {
	return t.scan("SELECT data FROM updates WHERE id>? AND requires_reply AND (item=?2 OR task=?2) ORDER BY id LIMIT ?", 0, item)
}

// Resolution is the update that resolved question id, by the operator or
// by their answer typed into the asking session's pane, or 0.
func (t *Tx) Resolution(id int) int {
	return t.reply(id, resolvedKinds)
}

// reply is the oldest update of one of kinds that replies to id, or 0.
func (t *Tx) reply(id int, kinds string) int {
	var n int
	if t.err == nil {
		err := t.q.QueryRowContext(t.ctx, "SELECT id FROM updates WHERE reply_to=? AND kind IN ("+kinds+") ORDER BY id LIMIT 1", id).Scan(&n)
		if !errors.Is(err, sql.ErrNoRows) {
			t.fail(err)
		}
	}
	return n
}

// QuestionStatuses are the statuses, as of now, of every question asked on
// item, oldest first: resolved once a resolution or pane answer replies to
// it, acknowledged once an acknowledgement does, and the replies counted.
func (t *Tx) QuestionStatuses(item string) []QuestionStatus {
	out := []QuestionStatus{}
	t.rows(func(rows *sql.Rows) error {
		var id int
		var kind sql.NullString
		var reply sql.NullInt64
		if err := rows.Scan(&id, &kind, &reply); err != nil {
			return err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, QuestionStatus{ID: id, State: "open"})
		}
		q := &out[len(out)-1]
		switch kind.String {
		case "reply":
			q.Replies++
		case "acknowledged":
			q.Acknowledged = true
		case "resolved", KindPaneAnswer:
			if q.Resolution == 0 {
				q.State, q.Resolution = "resolved", int(reply.Int64)
			}
		}
		return nil
	}, "SELECT q.id, r.kind, r.id FROM updates q LEFT JOIN updates r ON r.reply_to=q.id AND r.kind IN ('reply','acknowledged',"+resolvedKinds+")"+
		" WHERE q.item=? AND q.requires_reply ORDER BY q.id, r.id", item)
	return out
}

// Wrote reports whether author wrote any update: one indexed lookup.
func (t *Tx) Wrote(author string) bool {
	var n int
	if t.err == nil {
		t.fail(t.q.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM updates WHERE author=?)", author).Scan(&n))
	}
	return n == 1
}

// LatestUpdate is the newest update of kind about item.
func (t *Tx) LatestUpdate(item, kind string) (Update, bool) {
	return fetch[Update](t, "SELECT data FROM updates WHERE item=? AND kind=? ORDER BY id DESC LIMIT 1", item, kind)
}

// Host is what the coordinator saved of host; the zero record when nothing.
func (t *Tx) Host(name string) HostRecord {
	rec, _ := fetch[HostRecord](t, "SELECT data FROM hosts WHERE name=?", name)
	return rec
}

// PutHost saves rec as what the coordinator knows of host.
func (t *Tx) PutHost(name string, rec HostRecord) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO hosts(name,data) VALUES(?,?)", name, t.encode(rec))
}

// HostRecords are the saved host records by name.
func (t *Tx) HostRecords() map[string]HostRecord {
	out := map[string]HostRecord{}
	for _, row := range fetchAll[struct {
		Name string     `json:"name"`
		Rec  HostRecord `json:"rec"`
	}](t, "SELECT json_object('name', name, 'rec', json(data)) FROM hosts ORDER BY name") {
		out[row.Name] = row.Rec
	}
	return out
}

// PassAt is the last time the coordinator was known to run, zero before the
// first pass.
func (t *Tx) PassAt() time.Time {
	at, _ := fetch[time.Time](t, "SELECT data FROM marks WHERE name='pass'")
	return at
}

// SetPassAt saves the last time the coordinator was known to run.
func (t *Tx) SetPassAt(at time.Time) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO marks(name,data) VALUES('pass',?)", t.encode(at))
}

// Upgrade is the newest `engine upgrade` run's start and end.
func (t *Tx) Upgrade() UpgradeRecord {
	u, _ := fetch[UpgradeRecord](t, "SELECT data FROM marks WHERE name='upgrade'")
	return u
}

// SetUpgrade replaces the newest `engine upgrade` run's record.
func (t *Tx) SetUpgrade(u UpgradeRecord) {
	t.mustWrite()
	t.exec("INSERT OR REPLACE INTO marks(name,data) VALUES('upgrade',?)", t.encode(u))
}
