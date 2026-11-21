package telegram

import (
	"context"
	"fmt"
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
			run := Configured(home, control.Service{}, func(string, ...any) { logged = true })
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

func TestConfiguredLoopUsesExplicitDashboardAndPrivateToken(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "engine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"config.toml": "[telegram]\nchat=123\ndashboard_url='https://phone-ui.example.test:8443/'\n", TokenFile: strings.Repeat("x", 40)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := configured(home, control.Service{})
	if err != nil || l == nil || l.Chat != 123 || l.Dashboard != "https://phone-ui.example.test:8443/" || l.Bot.Token != strings.Repeat("x", 40) {
		t.Fatalf("loop %+v %v", l, err)
	}

	// A cancelled context proves the configured launcher does not outlive serve.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Configured(home, control.Service{}, nil)(ctx)
}

func TestMissingDashboardURLKeepsLoopAndButtonsAndLogsSetup(t *testing.T) {
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
	l, err := configured(home, control.Service{})
	if err != nil || l == nil || l.Chat != 123 || l.Dashboard != "" {
		t.Fatalf("loop %+v %v", l, err)
	}
	text, keys := question(control.Update{ID: 1, Item: "t1", Body: "Pick?", Choices: []string{"yes"}}, l.Dashboard)
	if strings.Contains(text, "http") || strings.HasSuffix(text, "\n") || len(keys.Rows) != 1 {
		t.Fatalf("rendered %q %+v", text, keys)
	}
	var log strings.Builder
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Configured(home, control.Service{}, func(format string, args ...any) { fmt.Fprintf(&log, format, args...) })(ctx)
	if !strings.Contains(log.String(), "dashboard links omitted; set [telegram] dashboard_url") {
		t.Fatal(log.String())
	}
}

func TestAlertPreservesConfiguredDashboardOrigin(t *testing.T) {
	l, f, plan := testLoop(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "engine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const dashboard = "https://phone-ui.example.test:8443/"
	for name, value := range map[string]string{"config.toml": "[server]\nurl='http://api.example.test:7331'\n[telegram]\nchat=99\ndashboard_url='" + dashboard + "'\n", TokenFile: strings.Repeat("x", 40)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configuredLoop, err := configured(home, l.Service)
	if err != nil {
		t.Fatal(err)
	}
	configuredLoop.Bot = l.Bot
	ask(t, l, plan)
	pass(t, configuredLoop, instant)
	text := f.calls["sendMessage"][0]["text"].(string)
	if !strings.HasSuffix(text, "\n\n"+dashboard) || strings.Contains(text, "api.example.test") {
		t.Fatal(text)
	}
}
