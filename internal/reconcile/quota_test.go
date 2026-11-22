package reconcile

import (
	"slices"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// reading is a harness's quota observed at offset from t0, with each window's
// used percent resetting an hour after t0 unless resets says otherwise.
func reading(harness string, offset time.Duration, used map[string]float64, resets ...time.Duration) session.Quota {
	reset := time.Hour
	if len(resets) > 0 {
		reset = resets[0]
	}
	q := session.Quota{Harness: harness, ObservedAt: t0.Add(offset).Unix(), Windows: map[string]session.QuotaWindow{}}
	for window, pct := range used {
		q.Windows[window] = session.QuotaWindow{Used: pct, ResetsAt: t0.Add(reset).Unix()}
	}
	return q
}

// quotaFleet is two hosts running claude and codex at a 1:1 ratio, each
// host reporting the readings given for it.
func quotaFleet(devbox, laptop []session.Quota) Fleet {
	f := Fleet{Now: t0, DefaultHarness: "claude", HarnessRatio: map[string]int{"claude": 1, "codex": 1},
		QuotaThresholds: map[string]int{"five_hour": 90, "seven_day": 95}}
	for _, h := range []struct {
		name string
		qs   []session.Quota
	}{{"devbox", devbox}, {"laptop", laptop}} {
		st := hostops.Status{Quota: map[string]session.Quota{}}
		for _, q := range h.qs {
			st.Quota[q.Harness] = q
		}
		f.Hosts = append(f.Hosts, HostLoad{Name: h.name, Harnesses: []string{"claude", "codex"}, Status: st})
	}
	return f
}

func TestHarnessesPreferRoom(t *testing.T) {
	both := []string{"claude", "codex"}
	swapped := []string{"codex", "claude"}
	for _, tc := range []struct {
		name    string
		f       Fleet
		running map[string]int
		want    []string
	}{
		{"no readings keep the ratio order", quotaFleet(nil, nil), nil, both},
		{"weekly at its threshold goes last", quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 95})}, nil), nil, swapped},
		{"weekly under its threshold keeps its place", quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 94.9})}, nil), nil, both},
		{"five-hour at its threshold goes last", quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"five_hour": 90, "seven_day": 10})}, nil), nil, swapped},
		{"a reading with no five-hour window is judged on its weekly", quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 50})}, nil), nil, both},
		{"a window past its reset counts as empty", quotaFleet([]session.Quota{reading("claude", -2*time.Hour, map[string]float64{"seven_day": 99}, -time.Minute)}, nil), nil, both},
		{"a window not configured is ignored", quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"monthly": 100})}, nil), nil, both},
		{"the newest reading across hosts decides", quotaFleet(
			[]session.Quota{reading("claude", -time.Hour, map[string]float64{"seven_day": 99})},
			[]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 20})}), nil, both},
		{"a near-full reading on another host demotes", quotaFleet(
			[]session.Quota{reading("claude", -time.Hour, map[string]float64{"seven_day": 20})},
			[]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 99})}), nil, swapped},
		{"the ratio orders the harnesses with room", quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 99})}, nil),
			map[string]int{"codex": 5}, swapped},
		{"every harness near full falls back to the ratio", quotaFleet([]session.Quota{
			reading("claude", 0, map[string]float64{"seven_day": 99}), reading("codex", 0, map[string]float64{"five_hour": 95})}, nil),
			map[string]int{"claude": 1}, swapped},
		{"every harness near full, ratio order stands", quotaFleet([]session.Quota{
			reading("claude", 0, map[string]float64{"seven_day": 99}), reading("codex", 0, map[string]float64{"five_hour": 95})}, nil), nil, both},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.f.harnesses(Want{}, tc.running)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("harnesses = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestHarnessesPinnedIgnoresQuota(t *testing.T) {
	f := quotaFleet([]session.Quota{reading("claude", 0, map[string]float64{"seven_day": 100})}, nil)
	got, err := f.harnesses(Want{Harness: "claude"}, nil)
	if err != nil || !slices.Equal(got, []string{"claude"}) {
		t.Fatalf("harnesses = %v, %v; want [claude]", got, err)
	}
}

// A near-full harness is passed over while another has room, and still
// takes the task when it is the only one that fits.
func TestPlacementPrefersAHarnessWithRoom(t *testing.T) {
	f := bothHarnesses(2)
	f.Now, f.QuotaThresholds = t0, map[string]int{"seven_day": 95}
	f.Hosts[0].Status.Quota = map[string]session.Quota{"claude": reading("claude", 0, map[string]float64{"seven_day": 97})}
	if got := f.Place([]Want{{ID: "t2"}}); len(got) != 1 || got[0].Harness != "codex" {
		t.Errorf("placement %+v, want codex", got)
	}
	f.Caps["codex"] = 0
	if got := f.Place([]Want{{ID: "t2"}}); len(got) != 1 || got[0].Harness != "claude" {
		t.Errorf("placement with codex at its cap %+v, want claude", got)
	}
}
