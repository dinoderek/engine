package controlcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
)

func TestClientServerOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, config, override, want, failure string
	}{
		{name: "default", want: "http://devbox:7331"},
		{name: "configured", config: "[server]\nurl = 'https://engine.example'\n", want: "https://engine.example"},
		{name: "override", config: "invalid", override: "http://127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		{name: "malformed", config: "invalid", failure: "config.toml"},
		{name: "wrong type", config: "[server]\nurl = 7331", failure: "must be a string"},
		{name: "unknown key", config: "[server]\nurl = 'http://devbox:7331'\nport = 7331", failure: "unknown key port"},
		{name: "invalid origin", config: "[server]\nurl = 'http://devbox:7331/api'", failure: "server URL must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, token := clientConfig(t, tc.config)
			client, err := makeClient(Env{Home: home, Getenv: func(key string) string {
				if key == "ENGINE_SERVER_URL" {
					return tc.override
				}
				return ""
			}})
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("error = %v, want %q", err, tc.failure)
				}
				return
			}
			if err != nil || client.URL != tc.want || client.Token != token {
				t.Fatalf("URL = %q, credential matches = %t, error = %v", client.URL, client.Token == token, err)
			}
		})
	}
}

func clientConfig(t *testing.T, config string) (home, token string) {
	t.Helper()
	home = t.TempDir()
	dir := hosts.ConfigDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	token = control.Secret()
	if err := os.WriteFile(filepath.Join(dir, "server-token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, token
}
