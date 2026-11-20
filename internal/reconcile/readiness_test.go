package reconcile

import (
	"strings"
	"testing"
)

// twoHosts is devbox with three free slots and mac with one, both running
// claude.
func twoHosts() Fleet {
	f := fleet(3)
	f.Hosts = append(f.Hosts, HostLoad{Name: "mac", Harnesses: []string{"claude"}, FreeSlots: 1, FreeHeavySlots: 1})
	return f
}

// A host whose probe failed for one repo takes none of its tasks, and still
// takes another repo's.
func TestPlacementSkipsAHostUnreadyForTheRepo(t *testing.T) {
	f := twoHosts()
	f.Hosts[0].Ready = map[string]Readiness{"r1": {Known: true, Unready: "gh auth status failed"}, "r2": {Known: true}}
	f.Hosts[1].Ready = map[string]Readiness{"r1": {Known: true}, "r2": {Known: true}}
	got := f.Place([]Want{{ID: "t1", Repo: "r1"}, {ID: "t2", Repo: "r2"}})
	if len(got) != 2 || got[0].Host != "mac" || got[1].Host != "devbox" {
		t.Errorf("placement %+v", got)
	}
}

// With no other host, the task waits, and the operator is told once by the
// placement notice, naming the failed check.
func TestPlacementOfAnUnreadyRepoNotifies(t *testing.T) {
	f := fleet(2)
	f.Hosts[0].Ready = map[string]Readiness{"r1": {Known: true, Unready: "gh auth status failed"}}
	got := f.Place([]Want{{ID: "t1", Repo: "r1"}})
	if len(got) != 1 || got[0].Host != "" || !got[0].Notify ||
		!strings.Contains(got[0].Wait, "devbox is unready for this repo: gh auth status failed") {
		t.Errorf("placement %+v", got)
	}
	v := view(plan("planned"), task(2, "todo"))
	v.Fleet = f
	if acts := shows(Decide(v)); acts != "notice t2 placement/3" {
		t.Errorf("actions %s", acts)
	}
}

// A host whose probe has not answered yet takes no task, and that is no
// notice: the probe is on its way.
func TestPlacementWaitsForAPendingProbeWithoutANotice(t *testing.T) {
	f := fleet(2)
	f.Hosts[0].Ready = map[string]Readiness{}
	got := f.Place([]Want{{ID: "t1", Repo: "r1"}})
	if len(got) != 1 || got[0].Host != "" || got[0].Notify || !strings.Contains(got[0].Wait, "devbox waits for its readiness probe") {
		t.Errorf("placement %+v", got)
	}
	f = twoHosts()
	f.Hosts[0].Ready = map[string]Readiness{}
	f.Hosts[1].Ready = map[string]Readiness{"r1": {Known: true}}
	if got := f.Place([]Want{{ID: "t1", Repo: "r1"}}); got[0].Host != "mac" {
		t.Errorf("a ready host waits for another's probe: %+v", got)
	}
	f.Hosts[1].Ready["r1"] = Readiness{Known: true, Unready: "./dev doctor failed"}
	if got := f.Place([]Want{{ID: "t1", Repo: "r1"}}); got[0].Notify ||
		!strings.Contains(got[0].Wait, "devbox waits for its readiness probe; mac is unready for this repo: ./dev doctor failed") {
		t.Errorf("one host pending, one unready: %+v", got)
	}
}

// A coordinator that does not probe leaves Ready nil, and every repo fits.
func TestPlacementWithoutProbesPlacesEveryRepo(t *testing.T) {
	if got := fleet(1).Place([]Want{{ID: "t1", Repo: "r1"}}); got[0].Host != "devbox" {
		t.Errorf("placement %+v", got)
	}
}
