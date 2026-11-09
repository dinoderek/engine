package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func TestCodexThreadTitle(t *testing.T) {
	tests := []struct {
		name string
		rec  session.Record
		want string
	}{
		{"a server task in a plan", session.Record{Control: true, Repo: "o/engine", Task: "o/engine#" + "34", Feature: "p12"}, "p12 > t34"},
		{"a server task in no plan", session.Record{Control: true, Repo: "o/engine", Task: "o/engine#" + "34"}, "t34"},
		{"an unparsable task stays as recorded", session.Record{Control: true, Repo: "o/engine", Task: "odd"}, "odd"},
	}
	for _, tt := range tests {
		if got := CodexThreadTitle(tt.rec); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// fakeDaemon answers JSON-RPC requests over a WebSocket peer from replies by
// method, and records each request's method and params.
type fakeDaemon struct {
	replies map[string][]string // method → successive raw replies ({"result":…} or {"error":…})
	calls   []string
	params  []map[string]any
}

func (f *fakeDaemon) serve(t *testing.T, peer *wsPeer) {
	for {
		_, p, err := peer.recv()
		if err != nil {
			return
		}
		var req struct {
			ID     *int           `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(p, &req); err != nil {
			t.Error(err)
			return
		}
		f.calls = append(f.calls, req.Method)
		f.params = append(f.params, req.Params)
		if req.ID == nil {
			continue
		}
		reply := `{"result":{}}`
		if r := f.replies[req.Method]; len(r) > 0 {
			reply, f.replies[req.Method] = r[0], r[1:]
		}
		// A notification first: the client must skip it.
		peer.send(t, true, 0x1, []byte(`{"method":"thread/name/updated","params":{}}`))
		id, _ := json.Marshal(*req.ID)
		peer.send(t, true, 0x1, []byte(`{"id":`+string(id)+`,`+strings.TrimPrefix(reply, "{")))
	}
}

func (f *fakeDaemon) exchange(t *testing.T, req CodexLabelRequest) error {
	t.Helper()
	c, peer, err := newWSPair(t, "101 Switching Protocols", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); f.serve(t, peer) }()
	err = exchangeCodexLabel(&codexRPC{c: c}, req)
	_ = peer.conn.Close()
	<-done
	return err
}

var labelReq = CodexLabelRequest{ThreadID: "th1", Title: "p12 > t138", Project: "engine", Repo: "o/engine", Root: "/clones/o/engine"}

func TestExchangeCodexLabel(t *testing.T) {
	ours := `{"result":{"data":[{"id":"pOurs","metadata":{"engine_repo":"o/engine"}}],"nextCursor":null}}`
	tests := []struct {
		name    string
		replies map[string][]string
		calls   []string
		project string // the projectId filed, or "" for none
		err     string
	}{
		{"an existing project, found on the second page", map[string][]string{"project/list": {
			`{"result":{"data":[{"id":"pOther","metadata":{"engine_repo":"o/app"}},{"id":"pMine","metadata":{}}],"nextCursor":"c2"}}`, ours}},
			[]string{"initialize", "initialized", "thread/name/set", "project/list", "project/list", "thread/metadata/update"}, "pOurs", ""},
		{"a new project", map[string][]string{"project/create": {`{"result":{"project":{"id":"pNew"}}}`}},
			[]string{"initialize", "initialized", "thread/name/set", "project/list", "project/create", "thread/metadata/update"}, "pNew", ""},
		{"a deleted project's key", map[string][]string{"project/create": {
			`{"error":{"code":-32602,"message":"idempotency key refers to deleted project: engine-o/engine"}}`, `{"result":{"project":{"id":"pAgain"}}}`}},
			[]string{"initialize", "initialized", "thread/name/set", "project/list", "project/create", "project/create", "thread/metadata/update"}, "pAgain", ""},
		{"a title the daemon refuses", map[string][]string{"thread/name/set": {`{"error":{"message":"no rollout found"}}`}},
			[]string{"initialize", "initialized", "thread/name/set"}, "", "title the thread"},
		{"a refused initialize", map[string][]string{"initialize": {`{"error":{"message":"nope"}}`}}, []string{"initialize"}, "", "initialize failed"},
		{"a create with no project", map[string][]string{"project/create": {`{"result":{}}`}},
			[]string{"initialize", "initialized", "thread/name/set", "project/list", "project/create"}, "", "without a project"},
		{"a list that fails", map[string][]string{"project/list": {`{"error":{"message":"experimental"}}`}},
			[]string{"initialize", "initialized", "thread/name/set", "project/list"}, "", "find or create project engine"},
		{"a filing the daemon refuses", map[string][]string{"project/create": {`{"result":{"project":{"id":"p"}}}`},
			"thread/metadata/update": {`{"error":{"message":"project not found"}}`}},
			[]string{"initialize", "initialized", "thread/name/set", "project/list", "project/create", "thread/metadata/update"}, "", "file the thread"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeDaemon{replies: tt.replies}
			err := f.exchange(t, labelReq)
			if tt.err == "" && err != nil || tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)) {
				t.Fatalf("err = %v, want %q", err, tt.err)
			}
			if !reflect.DeepEqual(f.calls, tt.calls) {
				t.Errorf("calls %v, want %v", f.calls, tt.calls)
			}
			if f.params[0]["capabilities"].(map[string]any)["experimentalApi"] != true {
				t.Error("initialize does not ask for the experimental API")
			}
			if len(f.params) > 2 && (f.params[2]["threadId"] != "th1" || f.params[2]["name"] != "p12 > t138") {
				t.Errorf("thread/name/set params %v", f.params[2])
			}
			last := f.params[len(f.params)-1]
			if tt.project != "" && (last["threadId"] != "th1" || last["projectId"] != tt.project) {
				t.Errorf("thread/metadata/update params %v, want project %s", last, tt.project)
			}
		})
	}
}

func TestExchangeCodexLabelCreatesTheRepoProject(t *testing.T) {
	f := &fakeDaemon{replies: map[string][]string{"project/create": {
		`{"error":{"message":"idempotency key refers to deleted project"}}`, `{"result":{"project":{"id":"p"}}}`}}}
	if err := f.exchange(t, labelReq); err != nil {
		t.Fatal(err)
	}
	var keys []any
	for i, c := range f.calls {
		if c != "project/create" {
			continue
		}
		p := f.params[i]
		keys = append(keys, p["idempotencyKey"])
		want := map[string]any{"name": "engine", "roots": []any{map[string]any{"path": "/clones/o/engine"}},
			"metadata": map[string]any{"engine_repo": "o/engine"}, "idempotencyKey": p["idempotencyKey"]}
		if !reflect.DeepEqual(p, want) {
			t.Errorf("project/create params %v, want %v", p, want)
		}
	}
	if want := []any{"engine-o/engine", "engine-o/engine-th1"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys %v, want %v", keys, want)
	}
}

func TestExchangeCodexLabelFailsWhenTheDaemonHangsUp(t *testing.T) {
	c, peer, err := newWSPair(t, "101 Switching Protocols", "")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _, _ = peer.recv(); peer.send(t, true, 0x8, nil) }()
	if err := exchangeCodexLabel(&codexRPC{c: c}, labelReq); err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Errorf("err = %v, want an initialize error", err)
	}
}

// labelDeps is a home with the host config, whose CodexLabel records each
// request and returns err.
func labelDeps(t *testing.T, err error) (Deps, *[]CodexLabelRequest, *[]string) {
	t.Helper()
	h := newHome(t, "mac")
	var reqs []CodexLabelRequest
	var where []string
	return Deps{Home: h.dir, Now: fixedNow, CodexLabel: func(_ context.Context, bin, home string, r CodexLabelRequest) error {
		reqs = append(reqs, r)
		where = append(where, bin, home)
		return err
	}}, &reqs, &where
}

func readLabel(t *testing.T, dir string) codexLabelRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, CodexLabelFile))
	if err != nil {
		t.Fatal(err)
	}
	var rec codexLabelRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestLabelCodexThreadStopsOnceLabelled(t *testing.T) {
	d, reqs, where := labelDeps(t, nil)
	dir := t.TempDir()
	rec := session.Record{Repo: "o/engine", Task: "o/engine#" + "138", Feature: "p12"}
	for range 2 {
		if err := LabelCodexThread(d, dir, rec, "th1"); err != nil {
			t.Fatal(err)
		}
	}
	want := CodexLabelRequest{ThreadID: "th1", Title: "p12 > t138", Project: "engine", Repo: "o/engine",
		Root: filepath.Join(d.Home, filepath.FromSlash(session.ReposDir), "o", "engine")}
	if !reflect.DeepEqual(*reqs, []CodexLabelRequest{want}) {
		t.Errorf("requests %+v, want one %+v", *reqs, want)
	}
	if bin, home := (*where)[0], (*where)[1]; bin != filepath.Join(d.Home, "bin", "codex") || home != (codex{d: d}).engineHome() {
		t.Errorf("sent with %s in %s", bin, home)
	}
	if got := readLabel(t, dir); got.CodexLabelRequest != want || got.Error != "" || got.Attempts != 1 || !got.At.Equal(fixedNow()) {
		t.Errorf("record %+v", got)
	}
	if CodexLabelPending(dir, fixedNow().Add(time.Hour)) {
		t.Error("a labelled run is still pending")
	}
	if _, err := os.Stat(filepath.Join(dir, codexLabelLock)); err == nil {
		t.Error("the lock was left behind")
	}
}

func TestLabelCodexThreadRetriesAFailureAFewTimes(t *testing.T) {
	d, reqs, _ := labelDeps(t, errors.New("daemon down"))
	dir := t.TempDir()
	rec := session.Record{Control: true, Repo: "o/engine", Task: "o/engine#" + "34", Feature: "p12"}
	now := fixedNow()
	d.Now = func() time.Time { return now }
	for i := range codexLabelAttempts {
		if !CodexLabelPending(dir, now) {
			t.Fatalf("attempt %d: not pending", i+1)
		}
		if err := LabelCodexThread(d, dir, rec, "th1"); err == nil || !strings.Contains(err.Error(), "daemon down") {
			t.Fatalf("attempt %d: err = %v", i+1, err)
		}
		// Too soon after a failure, nothing is tried.
		now = now.Add(codexLabelRetry - time.Second)
		if err := LabelCodexThread(d, dir, rec, "th1"); err != nil || CodexLabelPending(dir, now) || len(*reqs) != i+1 {
			t.Fatalf("attempt %d: retried too soon: err %v, %d requests", i+1, err, len(*reqs))
		}
		now = now.Add(time.Second)
	}
	if err := LabelCodexThread(d, dir, rec, "th1"); err != nil || len(*reqs) != codexLabelAttempts {
		t.Errorf("after the last attempt: err %v, %d requests, want %d", err, len(*reqs), codexLabelAttempts)
	}
	got := readLabel(t, dir)
	if got.Error != "daemon down" || got.Attempts != codexLabelAttempts || got.Title != "p12 > t34" ||
		got.Root != filepath.Join(d.Home, filepath.FromSlash(session.ControlReposDir), "o", "engine") {
		t.Errorf("record %+v", got)
	}
	if CodexLabelPending(dir, now.Add(time.Hour)) {
		t.Error("still pending after the last attempt")
	}
}

func TestLabelCodexThreadLeavesAHeldLockAlone(t *testing.T) {
	d, reqs, _ := labelDeps(t, nil)
	dir := t.TempDir()
	lock := filepath.Join(dir, codexLabelLock)
	write(t, lock, "", 0o644)
	rec := session.Record{Repo: "o/engine", Task: "o/engine#" + "1"}
	if err := LabelCodexThread(d, dir, rec, "th1"); err != nil || len(*reqs) != 0 {
		t.Fatalf("with a fresh lock: err %v, %d requests", err, len(*reqs))
	}
	// A lock older than any attempt's deadline is a dead attempt's.
	old := fixedNow().Add(-codexLabelStale - time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := LabelCodexThread(d, dir, rec, "th1"); err != nil || len(*reqs) != 1 {
		t.Errorf("with a stale lock: err %v, %d requests", err, len(*reqs))
	}
}

func TestLabelCodexThreadFailsWithoutItsInputs(t *testing.T) {
	tests := []struct {
		name string
		deps func(t *testing.T) Deps
		rec  session.Record
		want string
	}{
		{"a bad repo", func(t *testing.T) Deps { d, _, _ := labelDeps(t, nil); return d }, session.Record{Repo: "bad"}, "owner/repo"},
		{"no host config", func(t *testing.T) Deps { return Deps{Home: t.TempDir(), Now: fixedNow} }, session.Record{Repo: "o/engine", Task: "o/engine#" + "1"}, "hosts.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := LabelCodexThread(tt.deps(t), dir, tt.rec, "th1")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if got := readLabel(t, dir); got.Error == "" {
				t.Error("the failure was not recorded")
			}
		})
	}
	if err := LabelCodexThread(Deps{Now: fixedNow}, filepath.Join(t.TempDir(), "missing"), session.Record{}, "th1"); err == nil {
		t.Error("a missing run directory did not fail")
	}
}
