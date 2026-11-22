package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

const hostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model}"
tiers.deep     = { model = "opus", effort = "high" }
tiers.standard = { model = "sonnet", effort = "high" }
tiers.light    = { model = "haiku", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }

[host.devbox]
address = "dev@devbox"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }

[host.devbox.services]
manager = "systemd"
server = "engine-server.service"
`

var botToken = "123456:" + strings.Repeat("b", 35)

// home is the Mac's home with hosts.toml and, unless mode is 0, the token
// copy with that mode.
func home(t *testing.T, mode os.FileMode) string {
	t.Helper()
	h := t.TempDir()
	dir := filepath.Join(h, ".config", "engine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts.toml"), []byte(hostsTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if mode != 0 {
		path := filepath.Join(dir, "watch-telegram-token")
		if err := os.WriteFile(path, []byte(botToken+"\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

type caller struct {
	mu   sync.Mutex
	reqs []control.Request
	err  error
}

func (c *caller) Call(_ context.Context, r control.Request) (control.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, r)
	return control.Response{}, c.err
}

// bot is a fake Bot API that records each sendMessage.
func bot(t *testing.T) (*httptest.Server, *[]map[string]any) {
	var sent []map[string]any
	var mu sync.Mutex
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+botToken+"/sendMessage" {
			t.Errorf("bot got %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body)
		mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(s.Close)
	return s, &sent
}

// env runs passes passes, then ends.
func env(h string, c *caller, botURL string, passes int, errOut *strings.Builder) Env {
	n := 0
	return Env{
		Home: h, Err: errOut, TelegramURL: botURL,
		Connect: func() (Caller, error) { return c, nil },
		Now:     func() time.Time { return start },
		Context: context.Background(),
		Wait: func(context.Context, time.Duration) bool {
			n++
			return n < passes
		},
	}
}

func TestWatchAlertsTheChatWhenTheServerStopsAnswering(t *testing.T) {
	h := home(t, 0o600)
	s, sent := bot(t)
	c := &caller{err: errors.New("context deadline exceeded")}
	var errOut strings.Builder
	if code := Main([]string{"--chat", "42"}, env(h, c, s.URL, 3, &errOut)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if len(c.reqs) != 3 || c.reqs[0].Op != "list" || c.reqs[0].State != "repo" {
		t.Errorf("calls %+v", c.reqs)
	}
	if len(*sent) != 1 || (*sent)[0]["chat_id"] != float64(42) || !strings.HasPrefix((*sent)[0]["text"].(string), "Engine server on devbox is not answering") {
		t.Errorf("sent %v", *sent)
	}
	if !strings.Contains(errOut.String(), "calling the server on devbox every 1m0s") || strings.Contains(errOut.String(), botToken) {
		t.Errorf("stderr %q", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(h, StateFile)); err != nil {
		t.Error(err)
	}
}

func TestWatchRefusesBadFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"--chat", "x"}, {"--chat", "0"}, {"--chat", "1", "extra"}, {"--bogus"}} {
		var errOut strings.Builder
		if code := Main(args, Env{Err: &errOut}); code != 2 || !strings.Contains(errOut.String(), Usage) {
			t.Errorf("%q: exit %d, %q", args, code, errOut.String())
		}
	}
}

func TestWatchCannotStart(t *testing.T) {
	noServer := home(t, 0o600)
	if err := os.WriteFile(filepath.Join(noServer, ".config", "engine", "hosts.toml"), []byte(strings.Split(hostsTOML, "[host.devbox.services]")[0]), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, home, want string
		connect          error
	}{
		{"no hosts.toml", t.TempDir(), "hosts.toml", nil},
		{"no server host", noServer, "records a [host.<name>.services] table", nil},
		{"no token copy", home(t, 0), "the bot token copy", nil},
		{"an exposed token copy", home(t, 0o644), "readable only by its owner", nil},
		{"no client", home(t, 0o600), "no credential", errors.New("no credential")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errOut strings.Builder
			e := Env{Home: tc.home, Err: &errOut, Connect: func() (Caller, error) { return &caller{}, tc.connect }}
			if code := Main([]string{"--chat", "42"}, e); code != 1 || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("exit %d, %q; want %q", code, errOut.String(), tc.want)
			}
		})
	}
}
