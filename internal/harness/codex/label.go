package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// LabelFile, in a run directory, records the attempts to label the
// run's Codex thread and how the last one went; labelLock is held while
// one runs.
const (
	LabelFile = "codex-label.json"
	labelLock = "codex-label.lock"
)

// labelAttempts bounds the attempts per run, so a daemon that keeps
// refusing costs a few background processes, not one per hook event.
const labelAttempts = 3

// labelStale is how old a lock is when its holder must have died: an
// attempt ends within its deadline.
const labelStale = 30 * labelTimeout

// labelTimeout is how long one attempt may take.
const labelTimeout = 3 * time.Second

// LabelRequest is how a joined Codex thread is shown in the apps: its
// title, and the project it is filed under.
type LabelRequest struct {
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
	// Project is the project's name; Repo (owner/name) is the identity engine
	// finds it by, kept in the project's metadata, and Root its folder.
	Project string `json:"project"`
	Repo    string `json:"repo"`
	Root    string `json:"root"`
}

// labelRecord is LabelFile's content.
type labelRecord struct {
	LabelRequest
	At       time.Time `json:"at"`
	Attempts int       `json:"attempts"`
	Error    string    `json:"error,omitempty"`
}

func readLabelRecord(runDir string) (labelRecord, bool) {
	var rec labelRecord
	data, err := os.ReadFile(filepath.Join(runDir, LabelFile))
	return rec, err == nil && json.Unmarshal(data, &rec) == nil
}

// labelRetry spaces the attempts, so they span the daemon storing a new
// thread rather than all failing in its first seconds.
const labelRetry = 15 * time.Second

// LabelPending is true while the run's thread is not labelled, attempts
// remain and the last one is at least labelRetry before now.
func LabelPending(runDir string, now time.Time) bool {
	rec, ok := readLabelRecord(runDir)
	return !ok || rec.Error != "" && rec.Attempts < labelAttempts && now.Sub(rec.At) >= labelRetry
}

// projectKey is the project metadata key that holds the repo engine
// filed the project for.
const projectKey = "engine_repo"

// ThreadTitle is the title of a task's thread: "<plan> > <task>", or the
// task alone when it belongs to no plan. Runs name them by their server IDs
// (p12, t34).
func ThreadTitle(rec session.Record) string {
	task := rec.Task
	if t, err := session.ParseRef(rec.Task); err == nil {
		task = "t" + strconv.Itoa(t.Number)
	}
	if rec.Feature == "" {
		return task
	}
	return rec.Feature + " > " + task
}

// LabelThread titles a joined run's thread and files it under its repo's
// project, through engine's daemon, and records the outcome in LabelFile. It
// does nothing once the thread is labelled, after the last attempt, or while
// another attempt holds the lock. A failure is returned for the caller to
// report; it never stops the session.
func (a Adapter) LabelThread(runDir string, rec session.Record, threadID string) error {
	now := a.env.Time
	if !LabelPending(runDir, now()) {
		return nil
	}
	lock := filepath.Join(runDir, labelLock)
	if info, err := os.Stat(lock); err == nil && now().Sub(info.ModTime()) > labelStale {
		_ = os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = f.Close()
	defer func() { _ = os.Remove(lock) }()
	// Another attempt may have finished between the check and the lock.
	if !LabelPending(runDir, now()) {
		return nil
	}
	prev, _ := readLabelRecord(runDir)
	req, err := labelFor(a.env.Home, rec, threadID)
	if err == nil {
		err = a.sendLabel(req)
	}
	out := labelRecord{LabelRequest: req, At: now().UTC(), Attempts: prev.Attempts + 1}
	if err != nil {
		out.Error = err.Error()
	}
	data, merr := json.MarshalIndent(out, "", "  ")
	if merr == nil {
		merr = os.WriteFile(filepath.Join(runDir, LabelFile), data, 0o644)
	}
	return errors.Join(err, merr)
}

func labelFor(home string, rec session.Record, threadID string) (LabelRequest, error) {
	repo, err := session.ParseRepo(rec.Repo)
	if err != nil {
		return LabelRequest{}, err
	}
	root, err := session.Repos{Home: home, Control: rec.Control}.Path(repo)
	if err != nil {
		return LabelRequest{}, err
	}
	return LabelRequest{ThreadID: threadID, Title: ThreadTitle(rec), Project: repo.Name, Repo: repo.String(), Root: root}, nil
}

func (a Adapter) sendLabel(req LabelRequest) error {
	_, host, err := hosts.Open(hosts.ConfigDir(a.env.Home))
	if err != nil {
		return err
	}
	bin, err := host.BinPath(Name, a.env.Home)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), labelTimeout)
	defer cancel()
	return a.opts.Label(ctx, bin, a.engineHome(), req)
}

// labelThroughDaemon sets the title first, since thread/name/set is stable
// and the project calls are experimental.
func labelThroughDaemon(ctx context.Context, bin, home string, req LabelRequest) error {
	return codexDaemon(ctx, bin, home, func(c *wsConn) error {
		return exchangeLabel(&rpcClient{c: c}, req)
	})
}

func exchangeLabel(rpc *rpcClient, req LabelRequest) error {
	if _, err := rpc.call("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "engine", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return err
	}
	if err := rpc.notify("initialized"); err != nil {
		return err
	}
	if _, err := rpc.call("thread/name/set", map[string]any{"threadId": req.ThreadID, "name": req.Title}); err != nil {
		return fmt.Errorf("title the thread: %w", err)
	}
	id, err := findProject(rpc, req)
	if err != nil {
		return fmt.Errorf("find or create project %s: %w", req.Project, err)
	}
	if _, err := rpc.call("thread/metadata/update", map[string]any{"threadId": req.ThreadID, "projectId": id}); err != nil {
		return fmt.Errorf("file the thread under project %s: %w", req.Project, err)
	}
	return nil
}

// codexProject is the ID of the project engine filed for the repo, found by
// its metadata so a rename in the apps keeps it, or a new one.
func findProject(rpc *rpcClient, req LabelRequest) (string, error) {
	type project struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
	}
	params := map[string]any{}
	for {
		data, err := rpc.call("project/list", params)
		if err != nil {
			return "", err
		}
		var page struct {
			Data []project `json:"data"`
			Next *string   `json:"nextCursor"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return "", err
		}
		for _, p := range page.Data {
			if p.Metadata[projectKey] == req.Repo {
				return p.ID, nil
			}
		}
		if page.Next == nil || *page.Next == "" {
			break
		}
		params = map[string]any{"cursor": *page.Next}
	}
	// The key is per repo, so sessions that race create one project. Once
	// that project is deleted the key names it still, and the daemon refuses
	// it: a key per thread then creates the new one. The daemon gives that
	// refusal only the generic invalid-params code, so its wording is matched.
	create := func(key string) (json.RawMessage, error) {
		return rpc.call("project/create", map[string]any{
			"name": req.Project, "roots": []map[string]string{{"path": req.Root}},
			"metadata": map[string]string{projectKey: req.Repo}, "idempotencyKey": key,
		})
	}
	data, err := create("engine-" + req.Repo)
	if err != nil && strings.Contains(err.Error(), "deleted project") {
		data, err = create("engine-" + req.Repo + "-" + req.ThreadID)
	}
	if err != nil {
		return "", err
	}
	var created struct {
		Project project `json:"project"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.Project.ID == "" {
		return "", fmt.Errorf("project/create replied without a project: %s", data)
	}
	return created.Project.ID, nil
}

// rpcClient is a JSON-RPC client over the daemon's WebSocket.
type rpcClient struct {
	c    *wsConn
	next int
}

func (r *rpcClient) notify(method string) error {
	line, err := json.Marshal(map[string]any{"method": method, "params": map[string]any{}})
	if err != nil {
		return err
	}
	return r.c.WriteText(line)
}

// call sends a request and returns its result, skipping the notifications
// the daemon interleaves.
func (r *rpcClient) call(method string, params any) (json.RawMessage, error) {
	r.next++
	id := r.next
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := r.c.WriteText(line); err != nil {
		return nil, err
	}
	for {
		msg, err := r.c.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		var reply struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(msg, &reply); err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		if reply.ID == nil || *reply.ID != id || reply.Method != "" {
			continue
		}
		if len(reply.Error) != 0 {
			return nil, fmt.Errorf("%s failed: %s", method, reply.Error)
		}
		return reply.Result, nil
	}
}
