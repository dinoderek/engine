package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func TestCodexQuotaMapping(t *testing.T) {
	at := time.Unix(1793106000, 0)
	cases := []struct {
		name, body string
		windows    map[string]session.QuotaWindow
	}{
		{"both", `{"rateLimits":{"primary":{"usedPercent":24,"resetsAt":1793122362},"secondary":{"usedPercent":39,"resetsAt":1793603427}}}`, map[string]session.QuotaWindow{"five_hour": {Used: 24, ResetsAt: 1793122362}, "seven_day": {Used: 39, ResetsAt: 1793603427}}},
		{"partial", `{"rateLimits":{"primary":null,"secondary":{"usedPercent":0,"resetsAt":1793603427}}}`, map[string]session.QuotaWindow{"seven_day": {Used: 0, ResetsAt: 1793603427}}},
		{"missing percentage", `{"rateLimits":{"primary":{"resetsAt":1793122362}}}`, map[string]session.QuotaWindow{}},
		{"invalid windows", `{"rateLimits":{"primary":{"usedPercent":101,"resetsAt":1793122362},"secondary":{"usedPercent":-1,"resetsAt":1793603427}}}`, map[string]session.QuotaWindow{}},
		{"missing reset", `{"rateLimits":{"primary":{"usedPercent":10}}}`, map[string]session.QuotaWindow{}},
		{"null", `{"rateLimits":null}`, map[string]session.QuotaWindow{}},
		{"malformed", `{`, map[string]session.QuotaWindow{}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := codexQuota([]byte(tt.body), at)
			if got.Harness != Codex || got.ObservedAt != at.Unix() || !reflect.DeepEqual(got.Windows, tt.windows) {
				t.Fatalf("quota %+v, want %+v", got, tt.windows)
			}
		})
	}
}

func TestStatusCodexQuota(t *testing.T) {
	for _, kind := range []string{"success", "failed", "timeout", "partial", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			h := newHome(t, "mac")
			at := fixedNow()
			// A failed live call must not revive a persisted Codex observation.
			dir := writeRun(t, h, session.Record{RunID: "old", Name: "old", Harness: Codex, Spawned: at})
			write(t, filepath.Join(dir, session.QuotaFile), `{"harness":"codex","observed_at":1,"windows":{"five_hour":{"used":99,"resets_at":9999999999}}}`, 0600)
			called := false
			d := Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Exec: agentsFake("[]", nil), Now: func() time.Time { return at }, Getenv: func(string) string { return "/operator" }}
			d.CodexQuota = func(ctx context.Context, bin, home string) (json.RawMessage, error) {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing deadline")
				}
				if bin != filepath.Join(h.dir, "bin", "codex") || home != filepath.Join(h.dir, ".local/share/engine/codex") {
					t.Fatalf("wrong bin or home: %s %s", bin, home)
				}
				switch kind {
				case "failed":
					return nil, errors.New("not signed in")
				case "timeout":
					return nil, context.DeadlineExceeded
				case "partial":
					return json.RawMessage(`{"rateLimits":{"secondary":{"usedPercent":41,"resetsAt":9999999999}}}`), nil
				case "invalid":
					return json.RawMessage(`{`), nil
				default:
					return json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":83,"resetsAt":1},"secondary":{"usedPercent":41,"resetsAt":9999999999}}}`), nil
				}
			}
			got, err := ReadStatus(d)
			if err != nil || !called {
				t.Fatalf("status %+v, %v, called %v", got, err, called)
			}
			assertStatusCodexQuota(t, kind, got.Quota[Codex], at)
		})
	}
	h := newHome(t, "cl")
	_, err := ReadStatus(Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Exec: agentsFake("[]", nil), CodexQuota: func(context.Context, string, string) (json.RawMessage, error) {
		t.Fatal("Claude-only host asked Codex")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func assertStatusCodexQuota(t *testing.T, kind string, q session.Quota, at time.Time) {
	t.Helper()
	if kind != "success" && kind != "partial" {
		if q.Harness != "" {
			t.Fatalf("failed call kept quota %+v", q)
		}
		return
	}
	if q.Harness != Codex || q.ObservedAt != at.Unix() || q.Windows["seven_day"].Used != 41 {
		t.Fatalf("quota %+v", q)
	}
	if kind == "success" && q.Windows["five_hour"].Used != 0 {
		t.Fatal("expired window not cleared")
	}
	if kind == "partial" && len(q.Windows) != 1 {
		t.Fatalf("invented window %+v", q)
	}
}

func TestCodexQuotaExchange(t *testing.T) {
	cases := []struct {
		name, output string
		fail         bool
	}{
		{"success", "{\"id\":1,\"result\":{}}\n{\"method\":\"notice\"}\n{\"id\":9,\"result\":{}}\n{\"id\":2,\"result\":{\"rateLimits\":null}}\n", false},
		{"init error", "{\"id\":1,\"error\":{\"code\":1}}\n", true},
		{"read error", "{\"id\":1,\"result\":{}}\n{\"id\":2,\"error\":{\"code\":1}}\n", true},
		{"missing result", "{\"id\":1}\n", true},
		{"malformed", "no json\n", true},
		{"eof", "", true},
		{"too large", strings.Repeat("x", 1024*1024+1), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var sent bytes.Buffer
			got, err := exchangeCodexQuota(&sent, strings.NewReader(tt.output))
			if (err != nil) != tt.fail {
				t.Fatalf("reply %s, %v", got, err)
			}
			if !tt.fail && (!strings.Contains(sent.String(), `"method":"initialized"`) || !strings.Contains(sent.String(), `"method":"account/rateLimits/read"`)) {
				t.Fatal(sent.String())
			}
		})
	}
	for _, n := range []int{0, 1} {
		_, err := exchangeCodexQuota(&failQuotaWriter{remaining: n}, strings.NewReader("{\"id\":1,\"result\":{}}\n"))
		if err == nil {
			t.Fatal("write failure ignored")
		}
	}
}

type failQuotaWriter struct{ remaining int }

func (w *failQuotaWriter) Write(p []byte) (int, error) {
	if w.remaining == 0 {
		return 0, io.ErrClosedPipe
	}
	w.remaining--
	return len(p), nil
}

func TestCodexQuotaProcess(t *testing.T) {
	h := t.TempDir()
	bin := filepath.Join(h, "codex")
	write(t, bin, `#!/bin/sh
[ "$1 $2 $3" = 'app-server --listen stdio://' ] || exit 2
[ "$CODEX_HOME" = "$0.home" ] || exit 3
read -r init
printf '%s\n' '{"id":1,"result":{}}'
read -r initialized
read -r request
printf '%s\n' '{"id":2,"result":{"rateLimits":null}}'
# Remain alive until engine closes the client.
while read -r line; do :; done
`, 0755)
	ctx, cancel := context.WithTimeout(context.Background(), codexQuotaTimeout)
	defer cancel()
	got, err := readCodexRateLimits(ctx, bin, bin+".home")
	if err != nil || string(got) != `{"rateLimits":null}` {
		t.Fatalf("reply %s, %v", got, err)
	}
	if _, err := readCodexRateLimits(ctx, bin+"-missing", h); err == nil {
		t.Fatal("missing executable succeeded")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := readCodexRateLimits(canceled, bin, h); err == nil {
		t.Fatal("canceled call succeeded")
	}
}

func TestCodexQuotaSlowProcess(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	// Consume requests but never answer, even the initialization handshake.
	write(t, bin, "#!/bin/sh\nwhile read -r line; do :; done\n", 0755)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := readCodexRateLimits(ctx, bin, t.TempDir()); err == nil {
		t.Fatal("slow app-server succeeded")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("did not wait for deadline: %v", ctx.Err())
	}
}
