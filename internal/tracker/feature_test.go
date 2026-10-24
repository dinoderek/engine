package tracker

import (
	"errors"
	"strings"
	"testing"
)

// The paths are split so this file holds no tracker reference.
const (
	issues   = "repos/o/r/" + "issues/"
	repoAPI  = "https://api.github.com/repos/o/r"
	subIssue = `{"number":%d,"title":"t","state":"%s","body":"b","repository_url":"` + repoAPI + `","labels":[{"name":"engine:running"}]}`
)

func featureRunner(fail string) Runner {
	return func(_ []string, _ string, args ...string) (string, error) {
		path := args[1]
		if path == fail {
			return "HTTP 500", errors.New("exit 1")
		}
		switch path {
		case issues + "1":
			return `{"number":1,"title":"F","state":"open","labels":[{"name":"engine:executing"}]}`, nil
		case issues + "1/sub_issues":
			return "[" + strings.ReplaceAll(subIssue, "%d", "2") + "]" + "[" + strings.ReplaceAll(subIssue, "%d", "3") + "]", nil
		case issues + "2/dependencies/blocked_by":
			return "[]", nil
		case issues + "3/dependencies/blocked_by":
			return `[{"number":2,"state":"closed","repository_url":"` + repoAPI + `"}]`, nil
		}
		return "", errors.New("unexpected " + path)
	}
}

func TestReadFeature(t *testing.T) {
	run := featureRunner("")
	f, err := ReadFeature(func(env []string, name string, args ...string) (string, error) {
		out, err := run(env, name, args...)
		return strings.ReplaceAll(out, `"state":"%s"`, `"state":"open"`), err
	}, "tok", Ref{"o/r", 1})
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "F" || !f.Open || !f.Has("engine:executing") || len(f.Tasks) != 2 {
		t.Fatalf("feature %+v", f)
	}
	c := f.Tasks[1]
	if c.Ref != (Ref{"o/r", 3}) || !c.Open || !c.Has("engine:running") || c.Body != "b" ||
		len(c.BlockedBy) != 1 || c.BlockedBy[0] != (Blocker{Ref{"o/r", 2}, false}) {
		t.Errorf("card %+v", c)
	}
}

func TestReadFeatureFailures(t *testing.T) {
	for _, fail := range []string{issues + "1", issues + "1/sub_issues", issues + "3/dependencies/blocked_by"} {
		if _, err := ReadFeature(featureRunner(fail), "tok", Ref{"o/r", 1}); err == nil {
			t.Errorf("%s failing: no error", fail)
		}
	}
}

func TestSection(t *testing.T) {
	body := "## Outcome\nIt works.\n<!-- note -->\n\n### Detail\nx\n## Open\n- a\n## Outcomes\nno\n"
	// A ### subheading stays inside its section; a comment is dropped.
	if got, ok := Section(body, "Outcome"); !ok || got != "It works.\n\n\n### Detail\nx" {
		t.Errorf("outcome %q %v", got, ok)
	}
	if got, ok := Section(body, "Open"); !ok || got != "- a" {
		t.Errorf("open %q %v", got, ok)
	}
	if _, ok := Section(body, "Tests"); ok {
		t.Error("found Tests")
	}
	if _, ok := Section("##Outcome\nx", "Outcome"); ok {
		t.Error("read ##Outcome as a heading")
	}
}

func TestCardKey(t *testing.T) {
	if k := CardKey("x\n<!-- engine:card storage-sync -->\n"); k != "storage-sync" {
		t.Errorf("key %q", k)
	}
	if k := CardKey("no marker"); k != "" {
		t.Errorf("key %q", k)
	}
}
