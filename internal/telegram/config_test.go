package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

func TestIncompleteConfigDoesNotStartLoop(t *testing.T) {
	for _, tc := range []struct {
		name, config, token string
		mode                os.FileMode
		log                 bool
	}{
		{name: "no config"},
		{name: "no chat", token: strings.Repeat("a", 40), mode: 0o600},
		{name: "no token", config: "[telegram]\nchat=123"},
		{name: "bad token", config: "[telegram]\nchat=123", token: "short", mode: 0o600, log: true},
		{name: "exposed token", config: "[telegram]\nchat=123", token: strings.Repeat("a", 40), mode: 0o644, log: true},
		{name: "bad config", config: "[telegram]\nchat='bad'", log: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".config", "engine")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.token != "" {
				if err := os.WriteFile(filepath.Join(dir, TokenFile), []byte(tc.token), tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			logged := false
			run := Configured(home, control.Service{}, "http://server.test:7331", func(string, ...any) { logged = true })
			// A started loop would dereference the deliberately absent service store.
			run(context.Background())
			if logged != tc.log {
				t.Fatalf("logged %t", logged)
			}
		})
	}
}

func TestRetryAndCancellation(t *testing.T) {
	if got := retryDelay(&apiError{Retry: 90 * time.Second}, 10*time.Second); got != 90*time.Second {
		t.Fatal(got)
	}
	if got := retryDelay(context.Canceled, 10*time.Second); got != 10*time.Second {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wait(ctx, time.Hour) {
		t.Fatal("ignored cancellation")
	}
	if !wait(context.Background(), 0) {
		t.Fatal("zero wait")
	}
	l, _, _ := testLoop(t)
	l.Run(ctx)
}

func TestConfiguredLoopUsesServerHostAndPrivateToken(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "engine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"config.toml": "[telegram]\nchat=123\n", TokenFile: strings.Repeat("x", 40)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := configured(home, control.Service{}, "http://server.test:7331")
	if err != nil || l == nil || l.Chat != 123 || l.Dashboard != "http://server.test:7332" || l.Bot.Token != strings.Repeat("x", 40) {
		t.Fatalf("loop %+v %v", l, err)
	}
	if _, err = configured(home, control.Service{}, ":invalid"); err == nil {
		t.Fatal("invalid origin")
	}
	// A cancelled context proves the configured launcher does not outlive serve.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Configured(home, control.Service{}, "http://server.test:7331", nil)(ctx)
}
