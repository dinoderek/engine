package session

import (
	"reflect"
	"testing"
	"time"
)

func win(used float64, resetsAt int64) QuotaWindow {
	return QuotaWindow{Used: used, ResetsAt: resetsAt}
}

// quotaFleet is what three hosts report. Each harness is newest on a different
// host, one harness's newest reading is older than the other's, and some
// windows have reset by the fixed clock (1000).
func quotaFleet() []map[string]Quota {
	return []map[string]Quota{
		{
			"claude": {Harness: "claude", ObservedAt: 800, Windows: map[string]QuotaWindow{"five_hour": win(55, 999), "seven_day": win(20, 6000)}},
			"codex":  {Harness: "codex", ObservedAt: 600, Windows: map[string]QuotaWindow{"five_hour": win(95, 3000)}},
		},
		{
			"claude": {Harness: "claude", ObservedAt: 500, Windows: map[string]QuotaWindow{"five_hour": win(70, 2000)}},
			"codex":  {Harness: "codex", ObservedAt: 750, Windows: map[string]QuotaWindow{"five_hour": win(60, 1000), "seven_day": win(30, 5000)}},
		},
		{
			"claude": {Harness: "claude", ObservedAt: 700, Windows: map[string]QuotaWindow{"five_hour": win(99, 4000)}},
		},
	}
}

func TestMergeQuotaKeepsHarnessesIndependentAcrossHosts(t *testing.T) {
	got := MergeQuota(time.Unix(1000, 0), quotaFleet()...)
	want := map[string]Quota{
		// claude: host 1 is newest; its five_hour reset at 999, seven_day has not.
		"claude": {Harness: "claude", ObservedAt: 800, Windows: map[string]QuotaWindow{"five_hour": win(0, 999), "seven_day": win(20, 6000)}},
		// codex: host 2 is newest, though older than claude's. Its five_hour
		// resets exactly now, so it expires.
		"codex": {Harness: "codex", ObservedAt: 750, Windows: map[string]QuotaWindow{"five_hour": win(0, 1000), "seven_day": win(30, 5000)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged:\n got %+v\nwant %+v", got, want)
	}
	rev := quotaFleet()
	rev[0], rev[2] = rev[2], rev[0]
	if again := MergeQuota(time.Unix(1000, 0), rev...); !reflect.DeepEqual(again, want) {
		t.Fatalf("input order changed the result: %+v", again)
	}
}

func TestMergeQuotaReturnsCopiesOfWindows(t *testing.T) {
	inputs := quotaFleet()
	got := MergeQuota(time.Unix(1000, 0), inputs...)

	for _, q := range got {
		for name := range q.Windows {
			q.Windows[name] = win(-1, -1)
		}
		q.Windows["extra"] = win(-1, -1)
	}
	delete(got["claude"].Windows, "seven_day")

	if want := quotaFleet(); !reflect.DeepEqual(inputs, want) {
		t.Fatalf("mutating the result changed an input:\n got %+v\nwant %+v", inputs, want)
	}
}
