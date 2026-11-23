package control

// schemaVersion is the control database's schema. Versions 1 to 6 kept
// every record as JSON in one records table: version 2 stores only prepare,
// approved, planned and done for a plan; versions 1 and 2 held repository
// merge locks, and version 3 the merge queue instead; version 4 adds a
// question's choices, recommendation and class; version 5 Telegram delivery
// and poll state; version 6 each host's saved tmux start time and an
// attempt's host-wide loss mark. Version 7 keeps each kind of record in its
// own table, its row's filtered, joined and ordered fields in columns beside
// the whole record as data JSON. An older server refuses the newer database.
const schemaVersion = 7

// schema creates version 7's tables. Every row's data column is its whole
// record as JSON, so a new field needs no schema change; the other columns
// copy what a query filters, joins or orders by. Resolution, acknowledgement,
// the inbox and a session's latest message are queries over updates, never
// stored copies.
const schema = `CREATE TABLE items (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, repo TEXT NOT NULL DEFAULT '', plan TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT '', coordinating INTEGER NOT NULL DEFAULT 0, attempt TEXT NOT NULL DEFAULT '',
 slug TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL);
CREATE INDEX items_kind ON items(kind, slug);
CREATE INDEX items_repo ON items(repo);
CREATE INDEX items_plan ON items(plan);
CREATE INDEX items_state ON items(state);
CREATE INDEX items_coordinating ON items(coordinating);
CREATE INDEX items_attempt ON items(attempt);
CREATE TABLE item_deps (item TEXT NOT NULL, depends TEXT NOT NULL, PRIMARY KEY(item, depends)) WITHOUT ROWID;
CREATE INDEX item_deps_depends ON item_deps(depends);
CREATE TABLE item_requires (item TEXT NOT NULL, cap TEXT NOT NULL, PRIMARY KEY(item, cap)) WITHOUT ROWID;
CREATE TABLE attempts (
 id TEXT PRIMARY KEY, item TEXT NOT NULL, state TEXT NOT NULL, host TEXT NOT NULL DEFAULT '', data TEXT NOT NULL);
CREATE INDEX attempts_item ON attempts(item);
CREATE INDEX attempts_state ON attempts(state);
CREATE INDEX attempts_host ON attempts(host);
CREATE TABLE updates (
 id INTEGER PRIMARY KEY, item TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, author TEXT NOT NULL DEFAULT '',
 reply_to INTEGER NOT NULL DEFAULT 0, recipient TEXT NOT NULL DEFAULT '', task TEXT NOT NULL DEFAULT '',
 requires_reply INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL);
CREATE INDEX updates_item ON updates(item, id);
CREATE INDEX updates_task ON updates(task, id);
CREATE INDEX updates_reply ON updates(reply_to, kind);
CREATE INDEX updates_recipient ON updates(recipient, id);
CREATE INDEX updates_author ON updates(author);
CREATE TABLE queue (task TEXT PRIMARY KEY, repo TEXT NOT NULL, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE INDEX queue_repo ON queue(repo, seq);
CREATE TABLE credentials (hash TEXT PRIMARY KEY, attempt TEXT NOT NULL DEFAULT '', data TEXT NOT NULL);
CREATE INDEX credentials_attempt ON credentials(attempt);
CREATE TABLE receipts (
 key TEXT PRIMARY KEY, created TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
 attempt TEXT NOT NULL DEFAULT '', data TEXT NOT NULL);
CREATE INDEX receipts_created ON receipts(created);
CREATE INDEX receipts_attempt ON receipts(attempt);
CREATE TABLE hosts (name TEXT PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE counters (name TEXT PRIMARY KEY, value INTEGER NOT NULL);
INSERT INTO counters(name, value) VALUES('next', 1);
CREATE TABLE marks (name TEXT PRIMARY KEY, data TEXT NOT NULL);
`

// snapshotFields are the fields of an item an update's snapshot keeps: what
// its readers use. A held task's state before needs-human comes from the
// state; a Telegram message names the item by its title.
const snapshotFields = "'id', json_extract(data, '$.snapshot.id'), 'kind', json_extract(data, '$.snapshot.kind'), " +
	"'version', json_extract(data, '$.snapshot.version'), 'title', json_extract(data, '$.snapshot.title'), " +
	"'state', json_extract(data, '$.snapshot.state')"

// slim is the item snapshot an update keeps (snapshotFields).
func slim(item Item) *Item {
	return &Item{ID: item.ID, Kind: item.Kind, Version: item.Version, Title: item.Title, State: item.State}
}
