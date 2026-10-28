package hostops

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// codexTurnReadings reads only known engine threads in live windows. All
// requests share one deadline; a failed read leaves the hook state intact.
func codexTurnReadings(d Deps, host hosts.Host, windows []session.WindowInfo, runs map[string]session.Run) map[string]json.RawMessage {
	ids := []string{}
	for _, w := range windows {
		r, ok := runs[w.Name]
		if !ok || w.Dead || r.Record.Harness != Codex {
			continue
		}
		if st, ok := session.ReadHarnessState(r.Dir); ok && st.State != session.StateEnded && st.SessionID != "" && !st.At.IsZero() {
			ids = append(ids, st.SessionID)
		}
	}
	if len(ids) == 0 || !host.Runs(Codex) {
		return nil
	}
	bin, err := host.BinPath(Codex, d.Home)
	if err != nil {
		return nil
	}
	read := d.CodexTurns
	if read == nil {
		read = readCodexTurns
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexQuotaTimeout)
	defer cancel()
	c := codex{d: d}
	out := map[string]json.RawMessage{}
	// Embedded sessions may use the host's Codex home rather than engine's.
	for _, home := range slices.Compact([]string{c.engineHome(), c.home()}) {
		data, _ := read(ctx, bin, home, ids)
		for id, turn := range data {
			if slices.Contains(ids, id) {
				out[id] = turn
			}
		}
		ids = slices.DeleteFunc(ids, func(id string) bool { return len(out[id]) != 0 })
		if len(ids) == 0 || ctx.Err() != nil {
			break
		}
	}
	return out
}

func readCodexTurns(ctx context.Context, bin, home string, ids []string) (map[string]json.RawMessage, error) {
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
	if st.TurnID != "" && st.TurnID != t.ID {
		return false
	}
	return t.Status == "failed" && t.Error.Info == "usageLimitExceeded" && t.StartedAt > 0 &&
		t.CompletedAt != nil && *t.CompletedAt >= t.StartedAt && *t.CompletedAt >= st.At.Unix()
}

// codexStateLimited ignores a failure observed before this run's spawn.
func codexStateLimited(data json.RawMessage, st session.HarnessState, spawned time.Time) bool {
	return !st.At.Before(spawned) && codexTurnFailed(data, st)
}
