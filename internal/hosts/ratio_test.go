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

func TestValidateRatio(t *testing.T) {
	c := &Config{Harnesses: map[string]Harness{"claude": {}, "codex": {}}}
	if err := c.ValidateRatio(map[string]int{"claude": 4, "codex": 0}); err != nil {
		t.Fatal(err)
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
