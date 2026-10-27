package hostops

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

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

const codexQuotaTimeout = 3 * time.Second

func (s *Status) readCodexQuota(d Deps, host hosts.Host, now func() time.Time) {
	// Codex readings must come from the current call, never a stale run file.
	delete(s.Quota, Codex)
	if !host.Runs(Codex) {
		return
	}
	bin, err := host.BinPath(Codex, d.Home)
	if err != nil {
		return
	}
	read := d.CodexQuota
	if read == nil {
		read = readCodexRateLimits
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexQuotaTimeout)
	defer cancel()
	at := now()
	data, err := read(ctx, bin, (codex{d: d}).engineHome())
	if err != nil {
		return
	}
	q := codexQuota(data, at)
	if len(q.Windows) != 0 {
		s.Quota = session.MergeQuota(now(), s.Quota, map[string]session.Quota{Codex: q})
	}
}

// readCodexRateLimits starts no conversation. It closes the process and its
// pipes after the reply, including on malformed output or deadline expiry.
func readCodexRateLimits(ctx context.Context, bin, home string) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Close() }()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	stop := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer stop()
	return exchangeCodexQuota(in, out)
}

func exchangeCodexQuota(in io.Writer, out io.Reader) (json.RawMessage, error) {
	if _, err := io.WriteString(in, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"engine","version":"1"}}}`+"\n"); err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	if _, err := codexReply(scan, 1); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(in, `{"method":"initialized","params":{}}`+"\n"+`{"id":2,"method":"account/rateLimits/read"}`+"\n"); err != nil {
		return nil, err
	}
	return codexReply(scan, 2)
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
			return nil, fmt.Errorf("codex quota request failed: %s", reply.Error)
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
	q := session.Quota{Harness: Codex, ObservedAt: at.Unix(), Windows: map[string]session.QuotaWindow{}}
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
