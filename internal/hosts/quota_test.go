package hosts

import (
	"reflect"
	"strings"
	"testing"
)

func TestQuotaThresholdConfig(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want map[string]int
		err  string
	}{
		{"", map[string]int{"five_hour": 90, "seven_day": 95}, ""},
		{"quota_threshold = { seven_day = 80 }\n", map[string]int{"five_hour": 90, "seven_day": 80}, ""},
		{"quota_threshold = { five_hour = 100, seven_day = 1 }\n", map[string]int{"five_hour": 100, "seven_day": 1}, ""},
		{"quota_threshold = {}\n", map[string]int{"five_hour": 90, "seven_day": 95}, ""},
		{"quota_threshold = { seven_day = 101 }\n", nil, "seven_day must be at most 100"},
		{"quota_threshold = { five_hour = 0 }\n", nil, "five_hour must be at least 1"},
		{"quota_threshold = { monthly = 50 }\n", nil, "unknown key monthly"},
		{"quota_threshold = { seven_day = \"high\" }\n", nil, "must be an integer"},
		{"quota_threshold = 95\n", nil, "must be a table"},
	} {
		c, err := Parse(tc.src + base)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: %v", tc.src, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(c.QuotaThresholds, tc.want) {
			t.Errorf("%q: %v, %v", tc.src, c.QuotaThresholds, err)
		}
	}
}
