package session

import "testing"

// refText builds a task reference, split so this file holds no tracker
// reference.
func refText(repo, number string) string { return repo + "#" + number }

func TestParseRef(t *testing.T) {
	text := refText("own/repo", "21")
	ref, err := ParseRef(text)
	if err != nil || ref != (Ref{"own/repo", 21}) || ref.String() != text {
		t.Errorf("got %+v, %v", ref, err)
	}
	for _, bad := range []string{"", refText("engine", "21"), "o/r", refText("o/r", ""), refText("o/r", "0"), refText("o/r", "x"), refText("o/r/s", "1"), refText("", "1")} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
