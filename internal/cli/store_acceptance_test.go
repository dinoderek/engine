package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"

	_ "modernc.org/sqlite" // The driver the tests read and age the database with, beside the server.
)

// The acceptance tests of the SQL store: a real `engine serve` process, with
// no sqlite3 on its PATH, opening a copy of the version 6 fixture database
// that a version 6 server wrote, and migrating it on open.

// fixtureDir holds the version 6 fixture database and that server's answers.
const fixtureDir = "../control/testdata"

// fixtureDatabase is a new version 6 database at path holding the fixture.
func fixtureDatabase(t *testing.T, path string) string {
	t.Helper()
	script, err := os.ReadFile(filepath.Join(fixtureDir, "records-v6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, path, string(script))
	return path
}

// execSQL runs statements on the database at path through its own connection.
func execSQL(t *testing.T, path, statements string) {
	t.Helper()
	db := openSQL(t, path, false)
	if _, err := db.ExecContext(t.Context(), statements); err != nil {
		t.Fatal(err)
	}
}

// openSQL is a connection to the database at path, beside any server's.
func openSQL(t *testing.T, path string, readonly bool) *sql.DB {
	t.Helper()
	// The server's coordinator may be writing: wait for it, as the server does.
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=busy_timeout(5000)"
	if readonly {
		dsn += "&mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// scalar is the one value query returns on the database at path.
func scalar[T any](t *testing.T, path, query string) T {
	t.Helper()
	var v T
	if err := openSQL(t, path, true).QueryRowContext(t.Context(), query).Scan(&v); err != nil {
		t.Fatalf("%s on %s: %v", query, path, err)
	}
	return v
}

// noSQLite3 is a PATH of an empty directory: no sqlite3, nor any other tool.
func noSQLite3(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// serverProcess is a running `engine serve --foreground`, this test binary
// run as the engine command, under its own home.
type serverProcess struct {
	t      *testing.T
	home   string
	db     string
	token  string
	addr   string
	cmd    *exec.Cmd
	stderr *syncBuffer
	// client is the test's own, so stop can close its idle connections:
	// one left open would hold the server's shutdown past its deadline.
	client *http.Client
}

// serverHome is a home holding the operator credential at its default path,
// and no hosts.toml: the coordinator finds no host to act on.
func serverHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "engine"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, serverToken(home), control.Secret()+"\n")
	return home
}

// serverToken is the operator credential's default path under home.
func serverToken(home string) string { return filepath.Join(home, ".config", "engine", "server-token") }

// engineCommand is the engine command with args, under home, with PATH
// holding nothing, killed once ctx ends.
func engineCommand(ctx context.Context, t *testing.T, home string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, self, args...)
	var env []string
	for _, kv := range processEnv() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "HOME="+home, "PATH="+noSQLite3(t), engineProcessVar+"=1")
	return cmd
}

// startServer runs engine serve on db and waits for it to listen.
func startServer(t *testing.T, home, db string) *serverProcess {
	t.Helper()
	tokenFile := serverToken(home)
	token, err := control.ReadCredential(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	p := &serverProcess{t: t, home: home, db: db, token: token, stderr: &syncBuffer{}, client: &http.Client{Transport: &http.Transport{}}}
	p.cmd = engineCommand(context.Background(), t, home, "serve", "--foreground", "--db", db, "--token-file", tokenFile, "--listen", "127.0.0.1:0")
	p.cmd.Stderr = p.stderr
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.stop)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	m := regexp.MustCompile(`^engine server listening on (127\.0\.0\.1:\d+)\n$`).FindStringSubmatch(line)
	if err != nil || m == nil {
		t.Fatalf("engine serve printed %q (%v); stderr %s", line, err, p.stderr.String())
	}
	p.addr = m[1]
	return p
}

// stop signals the server as its unit manager does and expects a clean exit.
func (p *serverProcess) stop() {
	if p.cmd.ProcessState != nil {
		return
	}
	p.client.CloseIdleConnections()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			p.t.Errorf("engine serve exited with %v; stderr %s", err, p.stderr.String())
		}
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		p.t.Errorf("engine serve did not stop on SIGTERM; stderr %s", p.stderr.String())
	}
}

func (p *serverProcess) url() string { return "http://" + p.addr }

func (p *serverProcess) operator() control.Client {
	return control.Client{URL: p.url(), Token: p.token, HTTP: p.client}
}

// call runs r as the operator and fails t on an error.
func (p *serverProcess) call(t *testing.T, r control.Request) control.Response {
	t.Helper()
	out, err := p.operator().Call(t.Context(), r)
	if err != nil {
		t.Fatalf("%s %s: %v; stderr %s", r.Op, r.ID, err, p.stderr.String())
	}
	return out
}

// raw is the server's JSON answer to r, as the operator, undecoded.
func (p *serverProcess) raw(t *testing.T, r control.Request) []byte {
	t.Helper()
	body, _ := json.Marshal(r)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, p.url()+"/v1/command", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	res, err := p.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out bytes.Buffer
	if _, err = out.ReadFrom(res.Body); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s %v", r.Op, res.StatusCode, out.String(), err)
	}
	return out.Bytes()
}

// TestTheServerMigratesAVersion6DatabaseAndServesIt proves, against a real
// engine serve on the version 6 fixture, the store's objectives in order:
// the private backup and the migration on open, every record answering as
// the version 6 server answered, slimmed snapshots, reads beside a held
// write, writes and their receipts, retention, backup and the refusals.
func TestTheServerMigratesAVersion6DatabaseAndServesIt(t *testing.T) {
	dir := t.TempDir()
	db := fixtureDatabase(t, filepath.Join(dir, "control.db"))
	records := scalar[int](t, db, "SELECT count(*) FROM records")
	home := serverHome(t)
	p := startServer(t, home, db)

	t.Run("the open backs up and migrates", func(t *testing.T) { acceptMigrated(t, db, records) })
	t.Run("every record answers as before", func(t *testing.T) { acceptReads(t, p) })
	t.Run("snapshots hold only the kept fields", func(t *testing.T) { acceptSlimSnapshots(t, p, db) })
	t.Run("reads proceed beside a held write", func(t *testing.T) { acceptReadsBesideAWrite(t, p, db) })
	t.Run("writes and request keys", func(t *testing.T) { acceptWrites(t, p, db) })
	t.Run("receipts expire after seven days", func(t *testing.T) { acceptRetention(t, p, db) })
	p.stop()
	t.Run("backup and reopen", func(t *testing.T) { acceptBackupAndReopen(t, home, db) })
	t.Run("refusals", func(t *testing.T) { acceptRefusals(t, home, dir) })
}

// acceptMigrated checks the files the open left: an owner-only copy of the
// version 6 database, every record in it, and the database at version 7
// without its records table.
func acceptMigrated(t *testing.T, db string, records int) {
	backup := db + ".pre-v7"
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("no backup before the migration: %v", err)
	}
	check(t, info.Mode().Perm() == 0o600, "backup mode %v, want 0600", info.Mode().Perm())
	check(t, scalar[int](t, backup, "PRAGMA user_version") == 6, "the backup is not the version 6 database")
	check(t, scalar[int](t, backup, "SELECT count(*) FROM records") == records, "the backup lost records")
	check(t, scalar[int](t, db, "PRAGMA user_version") == 7, "the database was not migrated to version 7")
	check(t, scalar[int](t, db, "SELECT count(*) FROM sqlite_schema WHERE name='records'") == 0, "the records table survived the migration")
}

// fixtureAnswers are the fixture's read requests with the version 6 server's
// answers.
func fixtureAnswers(t *testing.T) []struct {
	Request  control.Request  `json:"request"`
	Response control.Response `json:"response"`
} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, "records-v6-reads.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reads []struct {
		Request  control.Request  `json:"request"`
		Response control.Response `json:"response"`
	}
	if err = json.Unmarshal(b, &reads); err != nil {
		t.Fatal(err)
	}
	return reads
}

// acceptReads sends every recorded read through the API. Each answer is the
// version 6 server's, except that a snapshot holds only its kept fields, an
// item's updates carry its question statuses, a plan view lists its tasks by
// ID rather than in map order, and the notices this server's coordinator has
// written since are its own.
func acceptReads(t *testing.T, p *serverProcess) {
	reads := fixtureAnswers(t)
	last := 0
	for _, r := range reads {
		for _, u := range r.Response.Updates {
			last = max(last, u.ID)
		}
	}
	for _, r := range reads {
		got := p.call(t, r.Request)
		if r.Request.Op == "updates" && r.Request.ID != "" {
			check(t, got.Questions != nil, "%s: an item's updates carry no question statuses", r.Request.ID)
			got.Questions = nil
		}
		got.Updates = slices.DeleteFunc(got.Updates, func(u control.Update) bool {
			check(t, u.ID <= last || u.Author == "coordinator", "%+v: update %d by %s is new", r.Request, u.ID, u.Author)
			return u.ID > last
		})
		want := normalized(r.Response)
		got = normalized(got)
		if g, w := jsonText(got), jsonText(want); g != w {
			t.Errorf("%+v through the migrated server:\n got %s\nwant %s", r.Request, g, w)
		}
	}
}

// normalized is r with its snapshots slimmed and its items by ID.
func normalized(r control.Response) control.Response {
	for i, u := range r.Updates {
		if s := u.Snapshot; s != nil {
			r.Updates[i].Snapshot = &control.Item{ID: s.ID, Kind: s.Kind, Version: s.Version, Title: s.Title, State: s.State}
		}
	}
	slices.SortStableFunc(r.Items, func(a, b control.Item) int { return cmp.Compare(control.Number(a.ID), control.Number(b.ID)) })
	return r
}

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }

// keptFields are the fields an update's item snapshot keeps.
var keptFields = []string{"id", "kind", "state", "title", "version"}

// acceptSlimSnapshots checks every update's snapshot, as the API returns it
// and as the database holds it: only the kept fields, with a state and a
// version.
func acceptSlimSnapshots(t *testing.T, p *serverProcess, db string) {
	var page struct {
		Updates []struct {
			ID       int            `json:"id"`
			Snapshot map[string]any `json:"snapshot"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(p.raw(t, control.Request{Op: "updates"}), &page); err != nil {
		t.Fatal(err)
	}
	stored := openSQL(t, db, true)
	snapshots := 0
	for _, u := range page.Updates {
		var data string
		if err := stored.QueryRowContext(t.Context(), "SELECT data FROM updates WHERE id=?", u.ID).Scan(&data); err != nil {
			t.Fatal(err)
		}
		var row struct {
			Snapshot map[string]any `json:"snapshot"`
		}
		if err := json.Unmarshal([]byte(data), &row); err != nil {
			t.Fatal(err)
		}
		for name, snap := range map[string]map[string]any{"returned": u.Snapshot, "stored": row.Snapshot} {
			if snap == nil {
				continue
			}
			snapshots++
			keys := slices.Sorted(func(yield func(string) bool) {
				for k := range snap {
					if !yield(k) {
						return
					}
				}
			})
			check(t, len(slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return slices.Contains(keptFields, k) })) == 0,
				"update %d's %s snapshot keeps %v", u.ID, name, keys)
			check(t, snap["state"] != nil && snap["version"] != nil, "update %d's %s snapshot has no state or version: %v", u.ID, name, snap)
		}
	}
	check(t, snapshots > 0, "no update carries a snapshot")
}

// acceptReadsBesideAWrite holds a write transaction open on the WAL database,
// as the server's writer does during a write, and reads through the server:
// each read answers from the last commit while the write is still open, so
// no read takes the write lock or sees what is uncommitted.
func acceptReadsBesideAWrite(t *testing.T, p *serverProcess, db string) {
	check(t, scalar[string](t, db, "PRAGMA journal_mode") == "wal", "the database is not in WAL mode")
	before := p.call(t, control.Request{Op: "get", ID: "p2"}).Item
	conn, err := openSQL(t, db, false).Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE; UPDATE items SET version=version+100 WHERE id='p2'"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []control.Request{{Op: "get", ID: "p2"}, {Op: "updates", ID: "t3"}, {Op: "inbox"}, {Op: "session-view"}, {Op: "overview"}, {Op: "plan-detail", ID: "p2"}} {
		out, err := p.operator().Call(t.Context(), r)
		check(t, err == nil, "%s %s while a write is held: %v", r.Op, r.ID, err)
		if r.Op == "get" && out.Item != nil {
			check(t, out.Item.Version == before.Version, "a read saw the uncommitted write: version %d, want %d", out.Item.Version, before.Version)
		}
	}
	if _, err = conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	p.call(t, control.Request{Op: "say", ID: "p2", Body: "Written once the other writer let go"})
}

// acceptWrites has a session write with the credential the version 6 server
// issued it, creates records and messages, and replays and reuses a key.
func acceptWrites(t *testing.T, p *serverProcess, db string) {
	credential := scalar[string](t, db, "SELECT json_extract(data, '$.response.credential') FROM receipts WHERE attempt='a8' AND json_extract(data, '$.response.credential') IS NOT NULL")
	sent, err := control.Client{URL: p.url(), Token: credential, HTTP: p.client}.Call(t.Context(), control.Request{Op: "say", ID: "t7", Body: "Still here", Key: control.Secret()})
	check(t, err == nil && len(sent.Updates) == 1 && sent.Updates[0].Author == "a8", "a8's migrated credential: %v %+v", err, sent)
	repo := p.call(t, control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "Other", Slug: "owner/other", Remote: "git@example.test:owner/other.git"}}).Item
	plan := p.call(t, control.Request{Op: "create", Item: control.Item{Kind: "plan", Repo: repo.ID, Title: "Second", Body: "Requirements"}}).Item
	check(t, p.call(t, control.Request{Op: "get", ID: plan.ID}).Item.Title == "Second", "the new plan does not read back")
	say := control.Request{Op: "say", ID: plan.ID, Body: "Hello", Key: control.Secret()}
	first := p.call(t, say)
	again := p.call(t, say)
	check(t, len(first.Updates) == 1 && jsonText(first) == jsonText(again), "a replayed key answered %s, then %s", jsonText(first), jsonText(again))
	say.Body = "Another message"
	_, err = p.operator().Call(t.Context(), say)
	var problem *control.Problem
	check(t, errors.As(err, &problem) && problem.Status == http.StatusConflict, "a reused key with another request: %v", err)
	thread := p.call(t, control.Request{Op: "updates", ID: plan.ID}).Updates
	check(t, len(thread) == 2 && thread[1].Body == "Hello", "the plan's thread: %+v", thread)
}

// acceptRetention ages every receipt past seven days and has one write sweep
// them: only the dispatch receipts of the attempts still dispatching stay,
// and an expired key is a new request.
func acceptRetention(t *testing.T, p *serverProcess, db string) {
	plan := p.call(t, control.Request{Op: "list", Item: control.Item{Kind: "plan"}}).Items[0]
	say := control.Request{Op: "say", ID: plan.ID, Body: "Before the sweep", Key: control.Secret()}
	p.call(t, say)
	execSQL(t, db, "UPDATE receipts SET created='2026-01-01T00:00:00.000Z'")
	p.call(t, control.Request{Op: "say", ID: plan.ID, Body: "This write sweeps"})
	rows, err := openSQL(t, db, true).QueryContext(t.Context(), "SELECT attempt FROM receipts WHERE created<'2026-01-02' ORDER BY attempt")
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for rows.Next() {
		var attempt string
		if err = rows.Scan(&attempt); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, attempt)
	}
	check(t, rows.Close() == nil && slices.Equal(kept, []string{"a4", "a6", "a8"}), "receipts kept after the sweep: %v, want the dispatches of a4, a6 and a8", kept)
	say.Body = "The same key, now expired"
	check(t, len(p.call(t, say).Updates) == 1, "an expired key did not run as a new request")
}

// acceptBackupAndReopen backs up the stopped server's database with engine
// serve --backup, through the driver with no sqlite3, and opens both again.
func acceptBackupAndReopen(t *testing.T, home, db string) {
	backup := filepath.Join(t.TempDir(), "backup.db")
	cmd := engineCommand(t.Context(), t, home, "serve", "--backup", backup, "--db", db)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("engine serve --backup: %v %s", err, out)
	}
	info, err := os.Stat(backup)
	check(t, err == nil && info.Mode().Perm() == 0o600, "backup %v %v", info, err)
	check(t, scalar[int](t, backup, "PRAGMA user_version") == 7, "the backup is not a version 7 database")
	want := scalar[int](t, db, "SELECT count(*) FROM updates")
	check(t, scalar[int](t, backup, "SELECT count(*) FROM updates") == want, "the backup lost updates")
	p := startServer(t, home, db)
	check(t, len(p.call(t, control.Request{Op: "updates"}).Updates) > 0, "the reopened server answers no updates")
	p.stop()
	check(t, scalar[int](t, db+".pre-v7", "PRAGMA user_version") == 6, "reopening touched the pre-v7 backup")
}

// acceptRefusals has engine serve refuse a database newer than it, and a
// version 6 database whose backup path is taken, naming the file.
func acceptRefusals(t *testing.T, home, dir string) {
	newer := filepath.Join(dir, "newer.db")
	execSQL(t, newer, "CREATE TABLE future (x); PRAGMA user_version=8;")
	taken := fixtureDatabase(t, filepath.Join(dir, "taken.db"))
	writeFile(t, taken+".pre-v7", "someone's file")
	for db, want := range map[string]string{newer: "schema 8 is newer than this server's 7", taken: taken + ".pre-v7"} {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		cmd := engineCommand(ctx, t, home, "serve", "--foreground", "--db", db, "--listen", "127.0.0.1:0")
		out, err := cmd.CombinedOutput()
		cancel()
		var exit *exec.ExitError
		check(t, errors.As(err, &exit) && exit.ExitCode() == 1 && strings.Contains(string(out), want), "serving %s: %v %s", db, err, out)
	}
	check(t, scalar[int](t, taken, "PRAGMA user_version") == 6, "a refused migration changed the database")
	if b, _ := os.ReadFile(taken + ".pre-v7"); string(b) != "someone's file" {
		t.Errorf("the refused migration overwrote %s", taken+".pre-v7")
	}
}
