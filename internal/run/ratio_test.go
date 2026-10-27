package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
)

func ratioFleet() Fleet {
	return Fleet{DefaultHarness: "claude", HarnessRatio: map[string]int{"claude": 4, "codex": 1}, Caps: map[string]int{"claude": 20, "codex": 20}, Hosts: []HostLoad{
		{Name: "mac", Harnesses: []string{"claude", "codex"}, FreeSlots: 20, Sessions: map[string]int{}},
	}}
}

func TestRatioStartsInOrderAndCountsPinnedCards(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		f := ratioFleet()
		var cards []tracker.Card
		for n := 2; n < 7; n++ {
			cards = append(cards, card(n, "standard"))
		}
		if pinned {
			cards[0] = onCodex(cards[0])
		}
		acts := place(View{Fleet: f}, cards)
		var got []string
		for _, a := range acts {
			got = append(got, a.Harness)
		}
		want := []string{"claude", "claude", "claude", "claude", "codex"}
		if pinned {
			want = []string{"codex", "claude", "claude", "claude", "claude"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("pinned %v: %v, want %v", pinned, got, want)
		}
	}
}

func TestRatioLiveLoadFallbackAndDefault(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Fleet)
		c    tracker.Card
		want string
	}{
		{"live load across hosts", func(f *Fleet) {
			f.Hosts = append(f.Hosts, HostLoad{Name: "other", Sessions: map[string]int{"claude": 4}})
		}, card(2, "standard"), "codex"},
		{"no host", func(f *Fleet) { f.Hosts[0].Harnesses = []string{"codex"} }, card(2, "standard"), "codex"},
		{"cap", func(f *Fleet) { f.Caps["claude"] = 0 }, card(2, "standard"), "codex"},
		{"slots", func(f *Fleet) {
			f.Hosts[0].FreeSlots = 0
			f.Hosts = append(f.Hosts, HostLoad{Name: "other", Harnesses: []string{"codex"}, FreeSlots: 1})
		}, card(2, "standard"), "codex"},
		{"capability", func(f *Fleet) {
			f.Hosts[0].Harnesses = []string{"claude"}
			f.Hosts = append(f.Hosts, HostLoad{Name: "other", Harnesses: []string{"codex"}, Capabilities: []string{"ios"}, FreeSlots: 1})
		}, needs(2, "standard", "ios"), "codex"},
		{"zero", func(f *Fleet) {
			f.HarnessRatio = map[string]int{"claude": 1, "codex": 0}
			f.Hosts[0].Sessions["claude"] = 10
		}, card(2, "standard"), "claude"},
		{"no ratio", func(f *Fleet) { f.HarnessRatio = nil; f.DefaultHarness = "codex" }, card(2, "standard"), "codex"},
		{"pinned zero", func(f *Fleet) { f.HarnessRatio = map[string]int{"claude": 1, "codex": 0} }, onCodex(card(2, "standard")), "codex"},
		{"default tie", func(f *Fleet) { f.HarnessRatio = map[string]int{"claude": 1, "codex": 1}; f.DefaultHarness = "codex" }, card(2, "standard"), "codex"},
		{"name tie", func(f *Fleet) { f.HarnessRatio = map[string]int{"claude": 1, "codex": 1}; f.DefaultHarness = "other" }, card(2, "standard"), "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ratioFleet()
			tc.edit(&f)
			acts := place(View{Fleet: f}, []tracker.Card{tc.c})
			if len(acts) != 1 || acts[0].Kind != StartTask || acts[0].Harness != tc.want {
				t.Fatalf("%+v, want %s", acts, tc.want)
			}
		})
	}
}

func TestRatioWaitsOnlyAfterAllChoicesFail(t *testing.T) {
	f := ratioFleet()
	f.Hosts[0].Harnesses = nil
	acts := place(View{Fleet: f}, []tracker.Card{card(2, "standard")})
	if len(acts) != 1 || !acts[0].Notify || !strings.Contains(acts[0].Text, "does not run claude") || !strings.Contains(acts[0].Text, "does not run codex") {
		t.Fatalf("%+v", acts)
	}
	f.Hosts[0].Harnesses = []string{"claude"}
	f.Caps["claude"] = 0
	acts = place(View{Fleet: f}, []tracker.Card{card(2, "standard")})
	if acts[0].Notify {
		t.Fatal("temporary cap wait notified")
	}
}

func TestApplyPassesTheSelectedHarnessToStart(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, card(2, "standard"))
	twoHosts(w, 2, 2)
	v := gather(t, w)
	v.Fleet.HarnessRatio = map[string]int{"codex": 1}
	acts := Decide(v)
	Apply(w, v, acts)
	if e := w.execs[ref(2)]; e == nil || e.harness != "codex" {
		t.Fatalf("selected harness not launched: %+v", e)
	}
	if counts := w.Fleet().running(); counts["codex"] != 1 {
		t.Fatalf("live counts %v", counts)
	}
}
