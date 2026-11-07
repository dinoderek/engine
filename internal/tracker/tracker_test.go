package tracker

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestLabelsAreWellFormed(t *testing.T) {
	color := regexp.MustCompile(`^[0-9a-f]{6}$`)
	seen := map[string]bool{}
	for _, l := range Labels {
		if !strings.HasPrefix(l.Name, "engine:") || seen[l.Name] || !color.MatchString(l.Color) || l.Description == "" || len(l.Description) > 100 {
			t.Errorf("bad label %+v", l)
		}
		seen[l.Name] = true
	}
	for _, state := range []string{"running", "needs-human", "in-review", "failed", "merging", "ruling-review", "approved", "blocked"} {
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
	if err := SyncLabels(run, "o/r", &out); err != nil {
		t.Fatal(err)
	}
	if len(calls) != len(Labels) {
		t.Fatalf("%d calls, want %d", len(calls), len(Labels))
	}
	want := " gh label create engine:feature --repo o/r --color 1d76db --description " + Labels[0].Description + " --force"
	if calls[0] != want {
		t.Errorf("first call %q, want %q", calls[0], want)
	}
	if !strings.Contains(out.String(), "14 engine labels in o/r") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestSyncLabelsFailures(t *testing.T) {
	if err := SyncLabels(nil, "not-a-repo", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "want owner/repo") {
		t.Errorf("bad repo: %v", err)
	}
	failing := func([]string, string, ...string) (string, error) { return "HTTP 403", errors.New("exit 1") }
	err := SyncLabels(failing, "o/r", &bytes.Buffer{})
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

func TestCurrentUser(t *testing.T) {
	for _, tc := range []struct {
		reply string
		fail  bool
	}{
		{"some-developer\n", false}, {"DifferentAccount", false}, {"managed_account", false}, {"", true},
		{"null", false}, {"two users", true}, {"-bad", true}, {"user\nother", true},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			run := func(env []string, name string, args ...string) (string, error) {
				if len(env) != 0 || name != "gh" || strings.Join(args, " ") != "api user --jq .login" {
					t.Fatalf("unexpected call %v %s %v", env, name, args)
				}
				return tc.reply, nil
			}
			got, err := CurrentUser(run)
			if (err != nil) != tc.fail || (!tc.fail && got != strings.TrimSpace(tc.reply)) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	if _, err := CurrentUser(func([]string, string, ...string) (string, error) { return "", errors.New("not logged in") }); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatal(err)
	}
}
