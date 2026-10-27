package hosts

import (
	"reflect"
	"strings"
	"testing"
)

func TestHarnessRatioConfig(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{ claude = 4 }`, ""}, {`{ claude = 0 }`, "positive weight"},
		{`{ missing = 1 }`, "not defined"}, {`{ claude = -1 }`, "at least 0"},
		{`{ claude = "four" }`, "must be an integer"}, {`{}`, "positive weight"},
		{`4`, "must be a table"},
	} {
		c, err := Parse("harness_ratio = " + tc.src + "\n" + base)
		if tc.want != "" {
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %v", tc.src, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(c.HarnessRatio, map[string]int{"claude": 4}) {
			t.Errorf("%s: %+v, %v", tc.src, c, err)
		}
	}
}

func TestParseAndValidateRatio(t *testing.T) {
	c := &Config{Harnesses: map[string]Harness{"claude": {}, "codex": {}}}
	got, err := ParseRatio("claude=4,codex=0")
	if err != nil || !reflect.DeepEqual(got, map[string]int{"claude": 4, "codex": 0}) || c.ValidateRatio(got) != nil {
		t.Fatalf("%v, %v", got, err)
	}
	for _, src := range []string{"", "claude", "=1", "claude=-1", "claude=x", "claude=1,", "claude=1,claude=2", "claude=9999999999999999999999999"} {
		if _, err := ParseRatio(src); err == nil {
			t.Errorf("accepted %q", src)
		}
	}
	for _, weights := range []map[string]int{{"claude": -1}, {"codex": 0}, {"unknown": 1}, {}} {
		if err := c.ValidateRatio(weights); err == nil {
			t.Errorf("accepted %v", weights)
		}
	}
	if err := c.ValidateRatio(nil); err != nil {
		t.Fatal(err)
	}
}
