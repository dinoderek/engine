package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// appServerTimeout is the deadline every app-server read of one status
// shares.
const appServerTimeout = 3 * time.Second

// readRateLimits starts no conversation. It closes the process and its
// pipes after the reply, including on malformed output or deadline expiry.
func readRateLimits(ctx context.Context, bin, home string) (json.RawMessage, error) {
	var data json.RawMessage
	err := codexAppServer(ctx, bin, home, func(in io.Writer, out io.Reader) error {
		var err error
		data, err = exchangeCodexQuota(in, out)
		return err
	})
	return data, err
}

func codexAppServer(ctx context.Context, bin, home string, exchange func(io.Writer, io.Reader) error) error {
	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	stop := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer stop()
	return exchange(in, out)
}

func exchangeCodexQuota(in io.Writer, out io.Reader) (json.RawMessage, error) {
	scan, err := initializeCodex(in, out)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(in, `{"id":2,"method":"account/rateLimits/read"}`+"\n"); err != nil {
		return nil, err
	}
	return codexReply(scan, 2)
}

func initializeCodex(in io.Writer, out io.Reader) (*bufio.Scanner, error) {
	if _, err := io.WriteString(in, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"engine","version":"1"}}}`+"\n"); err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	if _, err := codexReply(scan, 1); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(in, `{"method":"initialized","params":{}}`+"\n"); err != nil {
		return nil, err
	}
	return scan, nil
}

func codexReply(scan *bufio.Scanner, id int) (json.RawMessage, error) {
	for scan.Scan() {
		var reply struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(scan.Bytes(), &reply); err != nil {
			return nil, err
		}
		if reply.ID == nil || *reply.ID != id {
			continue
		}
		if len(reply.Error) != 0 || len(reply.Result) == 0 {
			return nil, fmt.Errorf("codex request failed: %s", reply.Error)
		}
		return reply.Result, nil
	}
	return nil, errors.Join(io.ErrUnexpectedEOF, scan.Err())
}

func codexQuota(data []byte, at time.Time) session.Quota {
	type window struct {
		Used  *float64 `json:"usedPercent"`
		Reset int64    `json:"resetsAt"`
	}
	var reply struct {
		Limits struct {
			Primary   *window `json:"primary"`
			Secondary *window `json:"secondary"`
		} `json:"rateLimits"`
	}
	q := session.Quota{Harness: Name, ObservedAt: at.Unix(), Windows: map[string]session.QuotaWindow{}}
	if json.Unmarshal(data, &reply) != nil {
		return q
	}
	for name, w := range map[string]*window{"five_hour": reply.Limits.Primary, "seven_day": reply.Limits.Secondary} {
		if w != nil && w.Used != nil && *w.Used >= 0 && *w.Used <= 100 && w.Reset > 0 {
			q.Windows[name] = session.QuotaWindow{Used: *w.Used, ResetsAt: w.Reset}
		}
	}
	return q
}

func readTurns(ctx context.Context, bin, home string, ids []string) (map[string]json.RawMessage, error) {
	data := map[string]json.RawMessage{}
	err := codexAppServer(ctx, bin, home, func(in io.Writer, out io.Reader) error {
		scan, err := initializeCodex(in, out)
		if err != nil {
			return err
		}
		for i, id := range ids {
			req := map[string]any{"id": i + 2, "method": "thread/turns/list", "params": map[string]any{
				"threadId": id, "limit": 1, "sortDirection": "desc", "itemsView": "notLoaded",
			}}
			line, err := json.Marshal(req)
			if err != nil {
				return err
			}
			if _, err := in.Write(append(line, '\n')); err != nil {
				return err
			}
			// A missing thread in this home must not hide other threads.
			if reply, err := codexReply(scan, i+2); err == nil {
				data[id] = reply
			}
		}
		return nil
	})
	return data, err
}

// codexTurnFailed compares the newest turn's completion with the latest hook.
// Epoch-second completion times share a second with the final tool hook.
// A newer submitted turn is returned first and so suppresses an old failure.
func codexTurnFailed(data json.RawMessage, st session.HarnessState) bool {
	var reply struct {
		Data []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			StartedAt   int64  `json:"startedAt"`
			CompletedAt *int64 `json:"completedAt"`
			Error       struct {
				Info string `json:"codexErrorInfo"`
			} `json:"error"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &reply) != nil || len(reply.Data) == 0 || st.At.IsZero() {
		return false
	}
	t := reply.Data[0]
	if st.TurnID != "" && st.TurnID != t.ID && t.StartedAt <= st.At.Unix() {
		return false
	}
	return t.Status == "failed" && t.Error.Info == "usageLimitExceeded" && t.StartedAt > 0 &&
		t.CompletedAt != nil && *t.CompletedAt >= t.StartedAt && *t.CompletedAt >= st.At.Unix()
}

// codexStateLimited ignores a failure observed before this run's spawn.
func codexStateLimited(data json.RawMessage, st session.HarnessState, spawned time.Time) bool {
	return !st.At.Before(spawned) && codexTurnFailed(data, st)
}
