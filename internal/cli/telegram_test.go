package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hosts"
)

type telegramTransport struct {
	target    *url.URL
	transport http.RoundTripper
}

func (r telegramTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	copy.URL = &u
	copy.URL.Scheme, copy.URL.Host = r.target.Scheme, r.target.Host
	return r.transport.RoundTrip(copy)
}

func TestSetupTelegramCommandConfirmsSenderBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name, input, response string
		code                  int
		chat                  int64
	}{
		{name: "confirm", input: "\ny\n", chat: 99},
		{name: "refuse", input: "\nn\n"},
		{name: "EOF", input: "", code: 1},
		{name: "no message", input: "\ny\n", response: `[]`, code: 1},
		{name: "group", input: "\ny\n", response: `[{"message":{"chat":{"id":99,"type":"group"}}}]`, code: 1},
		{name: "two messages", input: "\ny\n", response: `[{"message":{}},{"message":{}}]`, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, out, errOut, config := telegramSetupEnv(t, tc.input, tc.response)
			dir := hosts.ConfigDir(env.Home)
			if code := Run([]string{"setup", "telegram"}, env); code != tc.code {
				t.Fatalf("code %d: %s", code, errOut.String())
			}
			got, err := hosts.ReadTelegram(dir)
			if err != nil || got.Chat != tc.chat {
				t.Fatalf("chat %+v %v", got, err)
			}
			if tc.chat != 0 && !strings.Contains(out.String(), "Sender  (@operator), user 42; chat 99") {
				t.Fatal(out.String())
			}
			if tc.chat == 0 {
				data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
				if string(data) != config {
					t.Fatalf("refusal changed config %s", data)
				}
			}

		})
	}
	var out bytes.Buffer
	if code := Run([]string{"setup", "telegram", "--host", "remote"}, Env{Out: &out, Err: &out}); code != 2 {
		t.Fatalf("usage %d", code)
	}
}

func telegramSetupEnv(t *testing.T, input, response string) (Env, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	home := t.TempDir()
	dir := hosts.ConfigDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "telegram-token"), []byte(strings.Repeat("a", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "[server]\nurl = 'http://server.test'\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if response == "" {
		response = `[{"update_id":1,"message":{"message_id":1,"from":{"id":42,"first_name":"Sender","username":"operator"},"chat":{"id":99,"type":"private"}}}]`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, response)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: telegramTransport{target, server.Client().Transport}}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return Env{Home: home, In: strings.NewReader(input), Out: out, Err: errOut, TelegramHTTP: client}, out, errOut, config
}

func TestSetupTelegramRefusesAnExistingChat(t *testing.T) {
	env, _, errOut, _ := telegramSetupEnv(t, "\ny\n", "")
	if code := Run([]string{"setup", "telegram"}, env); code != 0 {
		t.Fatalf("setup %d: %s", code, errOut.String())
	}
	if code := Run([]string{"setup", "telegram"}, env); code != 1 || !strings.Contains(errOut.String(), "already configured") {
		t.Fatalf("reconfigure %d: %s", code, errOut.String())
	}
}
