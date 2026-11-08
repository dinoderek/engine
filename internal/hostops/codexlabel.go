package hostops

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

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// CodexLabelFile, in a run directory, records the attempts to label the
// run's Codex thread and how the last one went; codexLabelLock is held while
// one runs.
const (
	CodexLabelFile = "codex-label.json"
	codexLabelLock = "codex-label.lock"
)

// codexLabelAttempts bounds the attempts per run, so a daemon that keeps
// refusing costs a few background processes, not one per hook event.
const codexLabelAttempts = 3

// codexLabelStale is how old a lock is when its holder must have died: an
// attempt ends within its deadline.
const codexLabelStale = 30 * codexQuotaTimeout

// CodexLabelRequest is how a joined Codex thread is shown in the apps: its
// title, and the project it is filed under.
type CodexLabelRequest struct {
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
	// Project is the project's name; Repo (owner/name) is the identity engine
	// finds it by, kept in the project's metadata, and Root its folder.
	Project string `json:"project"`
	Repo    string `json:"repo"`
	Root    string `json:"root"`
}

// codexLabelRecord is CodexLabelFile's content.
type codexLabelRecord struct {
	CodexLabelRequest
	At       time.Time `json:"at"`
	Attempts int       `json:"attempts"`
	Error    string    `json:"error,omitempty"`
}

func readCodexLabel(runDir string) (codexLabelRecord, bool) {
	var rec codexLabelRecord
	data, err := os.ReadFile(filepath.Join(runDir, CodexLabelFile))
	return rec, err == nil && json.Unmarshal(data, &rec) == nil
}

// CodexLabelPending is true while the run's thread is not labelled and
// attempts remain.
func CodexLabelPending(runDir string) bool {
	rec, ok := readCodexLabel(runDir)
	return !ok || rec.Error != "" && rec.Attempts < codexLabelAttempts
}

// codexProjectKey is the project metadata key that holds the repo engine
// filed the project for.
const codexProjectKey = "engine_repo"

// CodexThreadTitle is the title of a task's thread: "<plan> > <task>", or the
// task alone when it belongs to no plan. A server run names them by their
// server IDs (p12, t34); a GitHub run by their issue numbers, with the repo
// only when the plan's differs from the task's.
func CodexThreadTitle(rec session.Record) string {
	task, plan := rec.Task, rec.Feature
	if t, err := tracker.ParseRef(rec.Task); err == nil {
		task = "#" + strconv.Itoa(t.Number)
		if rec.Control {
			task = "t" + strconv.Itoa(t.Number)
		}
	}
	if f, err := tracker.ParseRef(plan); err == nil && !rec.Control {
		plan = "#" + strconv.Itoa(f.Number)
		if f.Repo != rec.Repo {
			if r, err := session.ParseRepo(f.Repo); err == nil {
				plan = r.Name + plan
			}
		}
	}
	if plan == "" {
		return task
	}
	return plan + " > " + task
}

// LabelCodexThread titles a joined Codex run's thread and files it under its
// repo's project, through engine's daemon, and records the outcome in
// CodexLabelFile. It does nothing once the thread is labelled, after the last
// attempt, or while another attempt holds the lock. A failure is returned for
// the caller to report; it never stops the session.
func LabelCodexThread(d Deps, runDir string, rec session.Record, threadID string) error {
	if !CodexLabelPending(runDir) {
		return nil
	}
	lock := filepath.Join(runDir, codexLabelLock)
	if info, err := os.Stat(lock); err == nil && d.Now().Sub(info.ModTime()) > codexLabelStale {
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
	if !CodexLabelPending(runDir) {
		return nil
	}
	prev, _ := readCodexLabel(runDir)
	req, err := codexLabelFor(d.Home, rec, threadID)
	if err == nil {
		err = sendCodexLabel(d, req)
	}
	out := codexLabelRecord{CodexLabelRequest: req, At: d.Now().UTC(), Attempts: prev.Attempts + 1}
	if err != nil {
		out.Error = err.Error()
	}
	data, merr := json.MarshalIndent(out, "", "  ")
	if merr == nil {
		merr = os.WriteFile(filepath.Join(runDir, CodexLabelFile), data, 0o644)
	}
	return errors.Join(err, merr)
}

func codexLabelFor(home string, rec session.Record, threadID string) (CodexLabelRequest, error) {
	repo, err := session.ParseRepo(rec.Repo)
	if err != nil {
		return CodexLabelRequest{}, err
	}
	root, err := session.Repos{Home: home, Control: rec.Control}.Path(repo)
	if err != nil {
		return CodexLabelRequest{}, err
	}
	return CodexLabelRequest{ThreadID: threadID, Title: CodexThreadTitle(rec), Project: repo.Name, Repo: repo.String(), Root: root}, nil
}

func sendCodexLabel(d Deps, req CodexLabelRequest) error {
	_, host, err := openHost(d.Home)
	if err != nil {
		return err
	}
	bin, err := host.BinPath(Codex, d.Home)
	if err != nil {
		return err
	}
	send := d.CodexLabel
	if send == nil {
		send = labelCodexThread
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexQuotaTimeout)
	defer cancel()
	return send(ctx, bin, (codex{d: d}).engineHome(), req)
}

// labelCodexThread sets the title first, since thread/name/set is stable and
// the project calls are experimental.
func labelCodexThread(ctx context.Context, bin, home string, req CodexLabelRequest) error {
	return codexDaemon(ctx, bin, home, func(c *wsConn) error {
		return exchangeCodexLabel(&codexRPC{c: c}, req)
	})
}

func exchangeCodexLabel(rpc *codexRPC, req CodexLabelRequest) error {
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
	id, err := codexProject(rpc, req)
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
func codexProject(rpc *codexRPC, req CodexLabelRequest) (string, error) {
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
			if p.Metadata[codexProjectKey] == req.Repo {
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
			"metadata": map[string]string{codexProjectKey: req.Repo}, "idempotencyKey": key,
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

// codexRPC is a JSON-RPC client over the daemon's WebSocket.
type codexRPC struct {
	c    *wsConn
	next int
}

func (r *codexRPC) notify(method string) error {
	line, err := json.Marshal(map[string]any{"method": method, "params": map[string]any{}})
	if err != nil {
		return err
	}
	return r.c.WriteText(line)
}

// call sends a request and returns its result, skipping the notifications
// the daemon interleaves.
func (r *codexRPC) call(method string, params any) (json.RawMessage, error) {
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
