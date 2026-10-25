package run

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
)

// twoHosts is a world of the Mac and the devbox, with these many slots.
func twoHosts(w *world, mac, devbox int) {
	w.hosts = []fakeHost{
		{name: "mac", slots: mac, caps: []string{"macos", "ios", "vm", "docker"}, harnesses: []string{"claude", "codex"}},
		{name: "devbox", slots: devbox, caps: []string{"linux", "vm", "docker"}, harnesses: []string{"claude", "codex"}},
	}
}

// needs makes c require caps.
func needs(n int, tier string, caps ...string) tracker.Card {
	c := card(n, tier)
	c.Body = strings.Replace(c.Body, "## Requires\nnone", "## Requires\n"+strings.Join(caps, " "), 1)
	c.Requires = caps
	return c
}

// passOf decides one pass and applies it, and returns its actions.
func passOf(t *testing.T, w *world) []Action {
	t.Helper()
	v := gather(t, w)
	acts := Decide(v)
	Apply(w, v, acts)
	return acts
}

// waits are the pass's Wait actions, as <task number>: <text>.
func waits(acts []Action) []string {
	var out []string
	for _, a := range acts {
		if a.Kind == Wait {
			out = append(out, fmt.Sprintf("%d: %s", a.Ref.Number, a.Text))
		}
	}
	return out
}

func TestEachTaskGoesToTheHostWithTheMostFreeSlots(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned},
		card(2, "standard"), card(3, "standard"), card(4, "standard"), card(5, "standard"), card(6, "standard"))
	twoHosts(w, 1, 3)
	acts := passOf(t, w)
	// The devbox has the most free slots three times, the last time tied
	// with the Mac and won on fewer capabilities; then the Mac; then none.
	if want := []string{"2@devbox", "3@devbox", "4@devbox", "5@mac"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
	if got := waits(acts); len(got) != 1 || !strings.Contains(got[0], "6: no free slot on devbox (0 free), mac (0 free)") {
		t.Errorf("waits %q", got)
	}
}

func TestATieOnSlotsAndCapabilitiesGoesToTheLowestName(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, card(2, "standard"))
	w.hosts = []fakeHost{
		{name: "zeta", slots: 2, caps: []string{"linux"}, harnesses: []string{"claude"}},
		{name: "alpha", slots: 2, caps: []string{"macos"}, harnesses: []string{"claude"}},
	}
	passOf(t, w)
	if !slices.Equal(w.placed, []string{"2@alpha"}) {
		t.Errorf("placed %v", w.placed)
	}
}

func TestStartOrderHoldsAcrossHosts(t *testing.T) {
	// 3 is deep, so it starts first and takes the better host.
	w := newWorld(t, []string{"engine:feature", Planned}, card(2, "light"), card(3, "deep"))
	twoHosts(w, 1, 1)
	passOf(t, w)
	if want := []string{"3@devbox", "2@mac"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
}

func TestTheHarnessCapCountsEveryHost(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, card(2, "standard"), card(3, "standard"), card(4, "standard"))
	twoHosts(w, 3, 3)
	w.limit = 2
	if err := w.Start(ref(9), "devbox"); err != nil { // a session of another feature
		t.Fatal(err)
	}
	acts := passOf(t, w)
	if want := []string{"9@devbox", "2@mac"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
	want := []string{"3: claude is at its cap of 2 sessions", "4: claude is at its cap of 2 sessions"}
	if got := waits(acts); !slices.Equal(got, want) {
		t.Errorf("waits %q, want %q", got, want)
	}
	if len(w.comments[ref(3)]) != 0 {
		t.Errorf("a task waiting for the cap was commented on: %q", w.comments[ref(3)])
	}
}

func TestATaskGoesOnlyToAHostWithWhatItRequires(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, needs(2, "standard", "linux"), needs(3, "standard", "ios"))
	twoHosts(w, 3, 1)
	passOf(t, w)
	if want := []string{"2@devbox", "3@mac"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
}

func TestATaskNoHostCanTakeIsLoggedEachPassAndCommentedOnce(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, needs(2, "standard", "linux", "ios"), card(3, "standard"))
	twoHosts(w, 1, 1)
	w.hosts[1].harnesses = []string{"claude"}
	codex := needs(4, "standard", "linux")
	codex.Body = strings.Replace(codex.Body, "## Harness / tier\nstandard", "## Harness / tier\ncodex standard", 1)
	w.feature.Tasks = append(w.feature.Tasks, codex)
	first := waits(passOf(t, w))
	want := []string{
		"2: no host can take it: devbox lacks ios; mac lacks linux",
		"4: no host can take it: devbox does not run codex; mac lacks linux",
	}
	if !slices.Equal(first, want) {
		t.Errorf("waits %q, want %q", first, want)
	}
	if second := waits(passOf(t, w)); !slices.Equal(second, want) {
		t.Errorf("second pass waits %q", second)
	}
	for _, r := range []int{2, 4} {
		if c := w.comments[ref(r)]; len(c) != 1 || !strings.Contains(c[0], "@op ") || !strings.Contains(c[0], "mac lacks linux") {
			t.Errorf("#%d comments %q", r, c)
		}
	}
	logged := 0
	for _, l := range w.log {
		if strings.HasPrefix(l, "wait "+ref(2).String()+": no host can take it") {
			logged++
		}
	}
	if logged != 2 {
		t.Errorf("logged %d times, want 2:\n%s", logged, strings.Join(w.log, "\n"))
	}
	if !slices.Equal(w.placed, []string{"3@devbox"}) {
		t.Errorf("placed %v", w.placed)
	}
}

func TestACardWhoseHarnessNoHostDefinesWaits(t *testing.T) {
	bad := card(2, "standard")
	bad.Body = strings.Replace(bad.Body, "## Harness / tier\nstandard", "## Harness / tier\nstandrad", 1)
	twice := card(3, "standard")
	twice.Body = strings.Replace(twice.Body, "## Harness / tier\nstandard", "## Harness / tier\ndeep light", 1)
	w := newWorld(t, []string{"engine:feature", Executing}, bad, twice)
	got := waits(Decide(gather(t, w)))
	if len(got) != 2 || !strings.Contains(got[0], `defines no harness "standrad"`) || !strings.Contains(got[1], "names two tiers") {
		t.Errorf("waits %q", got)
	}
}

func TestAnUnreachableHostTakesNoTask(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, needs(2, "standard", "linux"), card(3, "standard"))
	twoHosts(w, 1, 3)
	w.hosts[1].down = "ssh: connect to host devbox: timed out"
	acts := passOf(t, w)
	if !slices.Equal(w.placed, []string{"3@mac"}) {
		t.Errorf("placed %v", w.placed)
	}
	got := waits(acts)
	if len(got) != 1 || !strings.Contains(got[0], "no free slot on devbox (unreachable: ssh: connect to host devbox: timed out)") {
		t.Errorf("waits %q", got)
	}
	if len(w.comments[ref(2)]) != 0 {
		t.Errorf("a task waiting for a host was commented on: %q", w.comments[ref(2)])
	}
}

func TestATaskStartedOnAnotherHostIsNotStartedAgainBeforeItClaims(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, needs(2, "standard", "linux"))
	twoHosts(w, 3, 3)
	passOf(t, w)
	if s := gather(t, w).Sessions[ref(2)]; s.Host != "devbox" || !s.Live {
		t.Fatalf("the devbox's session: %+v", s)
	}
	if acts := passOf(t, w); len(acts) != 0 {
		t.Errorf("second pass, before the claim: %+v", acts)
	}
	if !slices.Equal(w.placed, []string{"2@devbox"}) {
		t.Errorf("placed %v", w.placed)
	}
}

func TestThePlannerStartsOnlyOnThisHost(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	twoHosts(w, 0, 3)
	if acts := Decide(gather(t, w)); len(acts) != 0 {
		t.Errorf("the Mac is full, the devbox free: %+v", acts)
	}
}

func TestAFeatureRunsToDoneAcrossTwoHosts(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved},
		needs(2, "standard", "linux"), card(3, "standard"), card(4, "standard"), card(5, "standard", 2, 3, 4))
	twoHosts(w, 1, 1)
	for n := 2; n <= 5; n++ {
		w.do[n] = smooth
	}
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if !w.closed {
		t.Fatalf("not done:\n%s", strings.Join(w.log, "\n"))
	}
	if want := []string{"2@devbox", "3@mac", "4@devbox", "5@devbox"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
}

func TestAHeavyTaskWaitsForAHeavySlotWhileALightOneStarts(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, needs(2, "deep", "ios"), needs(3, "standard", "ios"), card(4, "standard"))
	w.hosts = []fakeHost{{name: "mac", slots: 3, heavy: 1, caps: []string{"macos", "ios"}, harnesses: []string{"claude"}}}
	for pass := 1; pass <= 2; pass++ {
		acts := passOf(t, w)
		if got := waits(acts); !slices.Equal(got, []string{"3: no free heavy slot on mac (0 free)"}) {
			t.Errorf("pass %d waits %q", pass, got)
		}
		// ready logs the task it holds back, every pass.
		if !slices.Contains(w.log, "wait "+ref(3).String()+": no free heavy slot on mac (0 free)") {
			t.Errorf("pass %d did not log the held-back task:\n%s", pass, strings.Join(w.log, "\n"))
		}
		w.log = nil
	}
	if want := []string{"2@mac", "4@mac"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
	if len(w.comments[ref(3)]) != 0 {
		t.Errorf("a task waiting for a heavy slot was commented on: %q", w.comments[ref(3)])
	}
}

func TestHeavyTasksGoWhereAHeavySlotIsFree(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned},
		needs(2, "deep", "vm"), needs(3, "standard", "vm"), needs(4, "standard", "vm"), needs(5, "standard", "vm"), card(6, "standard"))
	twoHosts(w, 3, 2)
	w.hosts[0].heavy = 1
	acts := passOf(t, w)
	// The Mac has the most free slots, but one heavy slot: heavy tasks
	// count only heavy slots, and fill the devbox first.
	if want := []string{"2@devbox", "3@devbox", "4@mac", "6@mac"}; !slices.Equal(w.placed, want) {
		t.Errorf("placed %v, want %v", w.placed, want)
	}
	if got := waits(acts); !slices.Equal(got, []string{"5: no free heavy slot on devbox (0 free), mac (0 free)"}) {
		t.Errorf("waits %q", got)
	}
}

func TestALightTaskHeldBackForSlotsIsLogged(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Planned}, card(2, "standard"), card(3, "standard"))
	w.slots = 1
	passOf(t, w)
	if !slices.Contains(w.log, "wait "+ref(3).String()+": no free slot on mac (0 free)") {
		t.Errorf("log:\n%s", strings.Join(w.log, "\n"))
	}
}
