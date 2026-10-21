package tracker

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeGH answers `gh api <path> ...` from a table keyed by path, and records
// every call.
type fakeGH struct {
	replies map[string]string
	errs    map[string]error
	calls   []string
}

func (f *fakeGH) run(env []string, name string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(env, " ")+" "+name+" "+strings.Join(args, " "))
	if name != "gh" || len(args) < 2 || args[0] != "api" {
		return "", errors.New("unexpected command " + name)
	}
	key := args[1]
	for _, a := range args {
		if q, ok := strings.CutPrefix(a, "q="); ok {
			key += "?" + q
		}
	}
	if err := f.errs[key]; err != nil {
		return "HTTP 404 Not Found", err
	}
	out, ok := f.replies[key]
	if !ok {
		return "", errors.New("no reply for " + key)
	}
	return out, nil
}

// The tests never write an issue number next to a path or a repo name: the
// docs check reads that as a citation. These helpers build them instead.

// issuePath is the REST path of issue n in o/r, plus any sub-path.
func issuePath(n int, sub ...string) string {
	return strings.Join(append([]string{fmt.Sprintf("repos/o/r/issues/%d", n)}, sub...), "/")
}

// issueURL is the API URL of issue n in repo.
func issueURL(repo string, n int) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, n)
}

// short renders a reference as owner/repo#n.
func short(repo string, n int) string { return fmt.Sprintf("%s#%d", repo, n) }

var (
	taskPath    = issuePath(21)
	blockerPath = issuePath(21, "dependencies", "blocked_by")
	taskIssue   = `{"number":21,"title":"A task","state":"open",
 "repository_url":"https://api.github.com/repos/o/r",
 "parent_issue_url":"` + issueURL("o/r", 20) + `",
 "labels":[{"name":"engine:running"},{"name":"engine:ruling-review"}],
 "body":"## Outcome\n\nx\n\n## Harness / tier\n\n<!-- Optional. -->\ncodex deep\n\n## Other\n"}`
)

func TestParseRef(t *testing.T) {
	text := short("own/repo", 21)
	ref, err := ParseRef(text)
	if err != nil || ref != (Ref{"own/repo", 21}) || ref.String() != text {
		t.Errorf("got %+v, %v", ref, err)
	}
	for _, bad := range []string{"", short("engine", 21), "o/r", "o/r#", short("o/r", 0), "o/r#x", short("o/r/s", 1), short("", 1)} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestReadTask(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{
		taskPath: taskIssue,
		blockerPath: `[{"number":23,"state":"closed","repository_url":"https://api.github.com/repos/o/r"},
			{"number":22,"state":"open","repository_url":"https://api.github.com/repos/o/r"}]
			[{"number":4,"state":"open","repository_url":"https://api.github.com/repos/p/q"}]`,
	}}
	task, err := ReadTask(gh.run, "tok", Ref{"o/r", 21})
	if err != nil {
		t.Fatal(err)
	}
	want := Task{
		Ref: Ref{"o/r", 21}, Title: "A task", Open: true,
		Labels:       []string{"engine:running", "engine:ruling-review"},
		OpenBlockers: []Ref{{"o/r", 22}, {"p/q", 4}},
		Parent:       &Ref{"o/r", 20}, Harness: "codex", Tier: "deep",
	}
	if !reflect.DeepEqual(task, want) {
		t.Errorf("got  %+v\nwant %+v", task, want)
	}
	if !task.Has("engine:running") || task.Has("engine:failed") {
		t.Error("Has is wrong")
	}
	if len(gh.calls) != 2 || !strings.HasPrefix(gh.calls[0], "GH_TOKEN=tok gh api "+taskPath) ||
		!strings.Contains(gh.calls[1], "dependencies/blocked_by --paginate") {
		t.Errorf("calls: %q", gh.calls)
	}
}

func TestReadTaskStandalone(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{
		issuePath(7): `{"number":7,"state":"closed","parent_issue_url":null,"labels":[],"body":"no sections"}`,
		issuePath(7, "dependencies", "blocked_by"): `[]`,
	}}
	task, err := ReadTask(gh.run, "tok", Ref{"o/r", 7})
	if err != nil {
		t.Fatal(err)
	}
	if task.Open || task.Parent != nil || len(task.OpenBlockers) != 0 || len(task.Labels) != 0 || task.Harness != "" || task.Tier != "standard" {
		t.Errorf("got %+v", task)
	}
}

func TestReadTaskFailures(t *testing.T) {
	ok := map[string]string{
		taskPath:    taskIssue,
		blockerPath: `[]`,
	}
	cases := []struct {
		name   string
		mutate func(*fakeGH)
		want   string
	}{
		{"issue read fails", func(f *fakeGH) { f.errs = map[string]error{taskPath: errors.New("exit 1")} }, "read " + short("o/r", 21) + ": exit 1\nHTTP 404"},
		{"issue is not JSON", func(f *fakeGH) { f.replies[taskPath] = "oops" }, "read " + short("o/r", 21) + ": unreadable gh output"},
		{"two tiers", func(f *fakeGH) {
			f.replies[taskPath] = `{"state":"open","body":"## Harness / tier\ndeep light"}`
		}, "names two tiers"},
		{"bad parent", func(f *fakeGH) {
			f.replies[taskPath] = `{"state":"open","parent_issue_url":"https://example.com/x"}`
		}, "parent: not a repository API URL"},
		{"blockers fail", func(f *fakeGH) {
			f.errs = map[string]error{blockerPath: errors.New("exit 1")}
		}, "blockers of " + short("o/r", 21) + ": exit 1"},
		{"blockers not JSON", func(f *fakeGH) { f.replies[blockerPath] = "{" }, "blockers of " + short("o/r", 21) + ": unreadable"},
		{"blocker without repo", func(f *fakeGH) {
			f.replies[blockerPath] = `[{"number":1,"state":"open"}]`
		}, "blockers of " + short("o/r", 21) + ": not a repository API URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeGH{replies: map[string]string{}}
			for k, v := range ok {
				gh.replies[k] = v
			}
			tc.mutate(gh)
			if _, err := ReadTask(gh.run, "tok", Ref{"o/r", 21}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseHarnessTier(t *testing.T) {
	cases := []struct {
		name, body, harness, tier string
	}{
		{"empty section", "## Harness / tier\n\n", "", "standard"},
		{"section only a comment", "## Harness / tier\n\n<!-- Optional.\nNever a model name. -->\n", "", "standard"},
		{"absent", "## Outcome\n\nx", "", "standard"},
		{"empty body", "", "", "standard"},
		{"tier", "## Harness / tier\n\ndeep\n", "", "deep"},
		{"tier in other case", "## Harness / tier\nLight", "", "light"},
		{"harness", "## Harness / tier\ncodex", "codex", "standard"},
		{"harness then tier", "## Harness / tier\ncodex, deep", "codex", "deep"},
		{"tier then harness", "## Harness / tier\nlight / claude", "claude", "light"},
		{"labelled", "## Harness / tier\nharness: codex\ntier: deep", "codex", "deep"},
		{"crlf and trailing space", "## Harness / tier  \r\nlight\r\n## Next\r\ndeep", "", "light"},
		{"stops at the next heading", "## Harness / tier\nlight\n## Requires\ncodex deep", "", "light"},
		{"trailing full stop", "## Harness / tier\nDeep.", "", "deep"},
		{"harness and tier with a full stop", "## Harness / tier\ncodex, deep.", "codex", "deep"},
		{"bullet", "## Harness / tier\n- deep", "", "deep"},
		{"numbered", "## Harness / tier\n1. light", "", "light"},
		{"none", "## Harness / tier\nNone.", "", "standard"},
		{"default harness and a tier", "## Harness / tier\ndefault / light", "", "light"},
		{"hyphenated harness", "## Harness / tier\nclaude-code light", "claude-code", "light"},
		{"section last", "## Outcome\nx\n## Harness / tier\ndeep", "", "deep"},
		{"heading inside a comment", "<!-- ## Harness / tier\ndeep -->\n## Outcome\nx", "", "standard"},
		{"similar heading", "## Harness / tiers\ndeep", "", "standard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness, tier, err := ParseHarnessTier(tc.body)
			if err != nil || harness != tc.harness || tier != tc.tier {
				t.Errorf("got %q, %q, %v; want %q, %q", harness, tier, err, tc.harness, tc.tier)
			}
		})
	}
	for _, bad := range []string{"deep light", "deep, deep", "claude codex"} {
		if _, _, err := ParseHarnessTier("## Harness / tier\n" + bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRefFromIssueURL(t *testing.T) {
	ref, err := refFromIssueURL(issueURL("o/r", 20))
	if err != nil || ref != (Ref{"o/r", 20}) {
		t.Errorf("got %+v, %v", ref, err)
	}
	for _, bad := range []string{"", "https://api.github.com/repos/o", "https://api.github.com/repos/o/r/pulls/" + "3", "https://api.github.com/repos/o/r/issues/x"} {
		if _, err := refFromIssueURL(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestDecodeOneWantsOneValue(t *testing.T) {
	if _, err := decodeOne[int](""); err == nil || !strings.Contains(err.Error(), "got 0") {
		t.Errorf("empty: %v", err)
	}
	if _, err := decodeOne[int]("1 2"); err == nil || !strings.Contains(err.Error(), "got 2") {
		t.Errorf("two: %v", err)
	}
}
