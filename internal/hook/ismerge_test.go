package hook

import "testing"

func TestIsMergeIncludesSafePinnedAndWrappedCommands(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"gh pr merge --squash --match-head-commit abc", true},
		{"engine gh pr merge --squash --match-head-commit abc", true},
		{"sh -c 'gh pr merge --match-head-commit abc'", true},
		{"eval gh pr merge --match-head-commit abc", true},
		{"echo $(gh pr merge --match-head-commit abc)", true},
		{"gh pr merge --help", false}, {"gh pr view", false}, {"echo hi", false},
	}
	for _, tc := range cases {
		if got := IsMerge(tc.command); got != tc.want {
			t.Errorf("%s: %v", tc.command, got)
		}
	}
}
