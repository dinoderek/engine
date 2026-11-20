package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	engineSkill = "plugins/engine/skills/engine/SKILL.md"
	serverRef   = "plugins/engine/skills/engine/references/server.md"
	buildSkill  = "plugins/engine/skills/build/SKILL.md"
	hardStops   = "plugins/engine/skills/build/references/hard-stops.md"
	reviewRef   = "plugins/engine/skills/build/references/review.md"
)

// skillRule is one rule the skills must state: the file that owns it and the
// exact sentence, compared after whitespace is collapsed so line wrapping
// does not matter.
type skillRule struct{ file, sentence string }

var skillRules = []skillRule{
	{engineSkill, "Plan and task IDs never appear in code, tests, docs, commit messages, PR titles or bodies."},
	{buildSkill, "Commit messages and PR bodies may, except in an engine server task"},
	{buildSkill, "Record every round's verdict, and your answer to each finding, in the PR body."},
	{hardStops, "A question with a safe, reversible default is not a stop: take the default and record it with `engine tell`."},
	{serverRef, "**Ask on your own task by default:**"},
	{serverRef, "**A plan question needs a proved-red default branch:**"},
	{serverRef, "**Host problems are the coordinator's readiness job, not a question.**"},
	{serverRef, "Request the merge only when `gh pr checks` shows nothing pending"},
	{serverRef, "never edit the PR body after `merge-ready`: it becomes the merge commit body."},
	{serverRef, "**Never say the operator agreed without citing the update that holds it.**"},
	{serverRef, "When the operator answers in the pane, the hook records it; do not re-post it."},
	{serverRef, "Post every reviewer verdict, APPROVED or not, with `engine tell <task>`, and list each round in the PR body."},
	{reviewRef, "The gate evidence in the PR body matches the CI run on the head."},
	{reviewRef, "No test is deleted or weakened and no skip is added without a Deviations entry."},
	{reviewRef, "Every operator attribution cites an update."},
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// missingSkillRules returns the rules whose sentence is absent from its file
// under root.
func missingSkillRules(root string) []string {
	var missing []string
	for _, r := range skillRules {
		b, err := os.ReadFile(filepath.Join(root, r.file))
		if err != nil || !strings.Contains(squash(string(b)), squash(r.sentence)) {
			missing = append(missing, r.file+": "+r.sentence)
		}
	}
	return missing
}

func TestSkillRulesPresent(t *testing.T) {
	if missing := missingSkillRules(filepath.Join("..", "..", "..")); len(missing) > 0 {
		t.Fatalf("skill rules missing:\n%s", strings.Join(missing, "\n"))
	}
}

func TestSkillRulesFailWhenASentenceIsRemoved(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	for _, r := range skillRules {
		t.Run(r.file+" "+r.sentence[:20], func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range []string{engineSkill, serverRef, buildSkill, hardStops, reviewRef} {
				b, err := os.ReadFile(filepath.Join(root, f))
				if err != nil {
					t.Fatal(err)
				}
				text := string(b)
				if f == r.file {
					text = strings.Replace(squash(text), squash(r.sentence), "", 1)
				}
				dst := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			missing := missingSkillRules(dir)
			if len(missing) == 0 || !strings.Contains(missing[0], r.sentence) {
				t.Fatalf("removing %q was not reported: %v", r.sentence, missing)
			}
		})
	}
}
