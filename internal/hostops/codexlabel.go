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

// CodexLabelFile, in a run directory, records that the run's Codex thread was
// labelled, and how it went. Its presence stops a second attempt.
const CodexLabelFile = "codex-label.json"

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
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
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
// repo's project, through engine's daemon. It runs once per run: the first
// call records the outcome in CodexLabelFile, and later calls do nothing.
// A failure is returned for the caller to report; it never stops the session.
func LabelCodexThread(d Deps, runDir string, rec session.Record, threadID string) error {
	marker := filepath.Join(runDir, CodexLabelFile)
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = f.Close()
	req, err := codexLabelFor(d.Home, rec, threadID)
	if err == nil {
		err = sendCodexLabel(d, req)
	}
	out := codexLabelRecord{CodexLabelRequest: req, At: d.Now().UTC()}
	if err != nil {
		out.Error = err.Error()
	}
	data, merr := json.MarshalIndent(out, "", "  ")
	if merr == nil {
		merr = os.WriteFile(marker, data, 0o644)
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
	// it: a key per thread then creates the new one.
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
