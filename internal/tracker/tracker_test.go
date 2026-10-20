package tracker

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestTokenPrefersTheEnvironment(t *testing.T) {
	token, err := Token(func(k string) string { return map[string]string{"GH_TOKEN": "env-token"}[k] }, t.TempDir())
	if err != nil || token != "env-token" {
		t.Errorf("got %q, %v", token, err)
	}
}

func TestTokenReadsTheFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, TokenFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := Token(noEnv, home)
	if err != nil || token != "file-token" {
		t.Errorf("got %q, %v", token, err)
	}
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Token(noEnv, home); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("empty file: %v", err)
	}
}

func TestTokenMissing(t *testing.T) {
	if _, err := Token(noEnv, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no GH_TOKEN and no token file") {
		t.Errorf("got %v", err)
	}
	if _, err := Token(noEnv, ""); err == nil || !strings.Contains(err.Error(), "no home directory") {
		t.Errorf("no home: %v", err)
	}
}

func TestLabelsAreWellFormed(t *testing.T) {
	color := regexp.MustCompile(`^[0-9a-f]{6}$`)
	seen := map[string]bool{}
	for _, l := range Labels {
		if !strings.HasPrefix(l.Name, "engine:") || seen[l.Name] || !color.MatchString(l.Color) || l.Description == "" || len(l.Description) > 100 {
			t.Errorf("bad label %+v", l)
		}
		seen[l.Name] = true
	}
	for _, state := range []string{"running", "needs-human", "in-review", "failed", "ruling-review", "approved", "blocked"} {
		if !seen["engine:"+state] {
			t.Errorf("no engine:%s label", state)
		}
	}
}

func TestSyncLabels(t *testing.T) {
	var calls []string
	run := func(env []string, name string, args ...string) (string, error) {
		calls = append(calls, strings.Join(env, " ")+" "+name+" "+strings.Join(args, " "))
		return "", nil
	}
	var out bytes.Buffer
	if err := SyncLabels(run, "tok", "o/r", &out); err != nil {
		t.Fatal(err)
	}
	if len(calls) != len(Labels) {
		t.Fatalf("%d calls, want %d", len(calls), len(Labels))
	}
	want := "GH_TOKEN=tok gh label create engine:feature --repo o/r --color 1d76db --description " + Labels[0].Description + " --force"
	if calls[0] != want {
		t.Errorf("first call %q, want %q", calls[0], want)
	}
	if !strings.Contains(out.String(), "13 engine labels in o/r") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestSyncLabelsFailures(t *testing.T) {
	if err := SyncLabels(nil, "tok", "not-a-repo", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "want owner/repo") {
		t.Errorf("bad repo: %v", err)
	}
	failing := func([]string, string, ...string) (string, error) { return "HTTP 403", errors.New("exit 1") }
	err := SyncLabels(failing, "tok", "o/r", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "label engine:feature") || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("gh failure: %v", err)
	}
}

func TestExec(t *testing.T) {
	out, err := Exec([]string{"ENGINE_TEST=yes"}, "sh", "-c", "echo $ENGINE_TEST")
	if err != nil || strings.TrimSpace(out) != "yes" {
		t.Errorf("got %q, %v", out, err)
	}
	if _, err := Exec(nil, "sh", "-c", "exit 3"); err == nil || !strings.Contains(err.Error(), "sh -c exit 3") {
		t.Errorf("failure: %v", err)
	}
}
