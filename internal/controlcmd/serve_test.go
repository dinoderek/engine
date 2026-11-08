package controlcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
)

func TestServeDefaultsToTheDocumentedPaths(t *testing.T) {
	e, _ := environment(t)
	tokenPath := filepath.Join(hosts.ConfigDir(e.Home), "server-token")
	if code := Main([]string{"serve"}, e); code != 1 || !strings.Contains(e.Err.(*bytes.Buffer).String(), tokenPath) {
		t.Fatalf("missing default credential: %d %s", code, e.Err)
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(control.Secret()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if code := Main([]string{"serve", "--backup", backup}, e); code != 0 {
		t.Fatalf("serve with defaults: %d %s", code, e.Err)
	}
	for _, path := range []string{filepath.Join(e.Home, ServerDBPath), backup} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}
