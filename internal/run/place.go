package run

import (
	"cmp"
	"errors"
	"fmt"
	"github.com/dinoderek/engine/internal/session"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/tracker"
)

// PlaceNoted is the note that the coordinator has commented that no host
// can take a task.
const PlaceNoted = "place-noted"

// Fleet is every host in hosts.toml as placement sees it this pass.
type Fleet struct {
	// Local is this host's name: the planner starts here.
	Local          string
	DefaultHarness string
	HarnessRatio   map[string]int
	// Caps is each harness's max_sessions, across all hosts.
	Caps map[string]int
	// Heavy are the capabilities that make a task heavy.
	Heavy []string
	// Hosts are sorted by name.
	Hosts []HostLoad
}

// HostLoad is one host: what it runs, and how full it is now.
type HostLoad struct {
	Name         string
	Harnesses    []string
	Capabilities []string
	FreeSlots    int
	// FreeHeavySlots is how many heavy sessions the host can start now.
	FreeHeavySlots int
	// Sessions counts the live sessions of each harness on the host.
	Sessions map[string]int
	// Live names the host's live windows.
	Live []string
	// Unreachable says why the host's status could not be read. Such a host
	// takes no task, and its sessions are unknown.
	Unreachable string
	// Status is the host's reply, which the session check reads.
	Status hostops.Status
}

// load is a host's load from its status: its free slots, heavy and all, and
// its live sessions by the harness their newest run names.
func load(h hosts.Host, st hostops.Status, err error) HostLoad {
	l := HostLoad{Name: h.Name, Harnesses: h.Harnesses, Capabilities: h.Capabilities, Sessions: map[string]int{}}
	if err != nil {
		l.Unreachable = err.Error()
		return l
	}
	l.FreeSlots, l.FreeHeavySlots, l.Status = st.FreeSlots, st.FreeHeavySlots, st
	harness := map[string]string{}
	for _, r := range st.Runs {
		harness[r.Name] = r.Record.Harness
	}
	for _, w := range st.Windows {
		if !w.Alive {
			continue
		}
		l.Live = append(l.Live, w.Name)
		if hn := harness[w.Name]; hn != "" {
			l.Sessions[hn]++
		}
	}
	return l
}

func (h HostLoad) has(capability string) bool { return slices.Contains(h.Capabilities, capability) }

// free is how many slots the host has free now, and how many of them a heavy
// task may take: none when it cannot be read.
func (h HostLoad) free() slots {
	if h.Unreachable != "" {
		return slots{}
	}
	all := max(0, h.FreeSlots)
	return slots{all: all, heavy: min(all, max(0, h.FreeHeavySlots))}
}

// slots are a host's free slots as placement hands them out.
type slots struct{ all, heavy int }

// take is the slots left once a task starts.
func (s slots) take(heavy bool) slots {
	s.all--
	if heavy {
		s.heavy--
	}
	s.heavy = min(s.heavy, s.all)
	return s
}

// left is how many tasks like this one the host can still start.
func (s slots) left(heavy bool) int {
	if heavy {
		return s.heavy
	}
	return s.all
}

// host finds a host by name.
func (f Fleet) host(name string) (HostLoad, bool) {
	i := slices.IndexFunc(f.Hosts, func(h HostLoad) bool { return h.Name == name })
	if i < 0 {
		return HostLoad{}, false
	}
	return f.Hosts[i], true
}

// localFree is how many slots this host has free.
func (f Fleet) localFree() int {
	for _, h := range f.Hosts {
		if h.Name == f.Local {
			return h.free().all
		}
	}
	return 0
}

// liveAnywhere reports whether a host that answered has a live window
// named name.
func (f Fleet) liveAnywhere(name string) bool {
	return slices.ContainsFunc(f.Hosts, func(h HostLoad) bool { return slices.Contains(h.Live, name) })
}

// running counts the live sessions of each harness on the hosts that
// answered.
func (f Fleet) running() map[string]int {
	out := map[string]int{}
	for _, h := range f.Hosts {
		for harness, n := range h.Sessions {
			out[harness] += n
		}
	}
	return out
}

// place is step 3's placement: it walks the ready tasks in start order and
// gives each the eligible host with the most free slots, then the fewest
// capabilities, then the lowest name. A heavy task counts only a host's free
// heavy slots. A task no host can take now waits, with the reason; one no
// host could take even when free is commented on once.
func place(v View, cands []tracker.Card) []Action {
	free := map[string]slots{}
	for _, h := range v.Fleet.Hosts {
		free[h.Name] = h.free()
	}
	running := v.Fleet.running()
	var acts []Action
	for _, c := range cands {
		harness, best, why, notify := v.Fleet.choose(c, free, running)
		if why != "" {
			acts = append(acts, Action{Kind: Wait, Ref: c.Ref, Text: why, Notify: notify && !v.PlaceNoted[c.Ref]})
			continue
		}
		heavy := v.Fleet.heavy(c)
		free[best.Name] = free[best.Name].take(heavy)
		running[harness]++
		acts = append(acts, Action{Kind: StartTask, Ref: c.Ref, Host: best.Name, Harness: harness, Text: "ready, on " + best.Name})
	}
	return acts
}

// fits returns the card's harness and the hosts that could take it if they
// had a free slot. When none could, why says what each host lacks.
func (f Fleet) fits(c tracker.Card, harness string) (fits []HostLoad, why string) {
	if _, ok := f.Caps[harness]; !ok {
		return nil, fmt.Sprintf("hosts.toml defines no harness %q (a misspelt tier? tiers are deep, standard and light)", harness)
	}
	var lacks []string
	for _, h := range f.Hosts {
		missing := slices.DeleteFunc(slices.Clone(c.Requires), h.has)
		switch {
		case !slices.Contains(h.Harnesses, harness):
			lacks = append(lacks, fmt.Sprintf("%s does not run %s", h.Name, harness))
		case len(missing) > 0:
			lacks = append(lacks, fmt.Sprintf("%s lacks %s", h.Name, strings.Join(missing, ", ")))
		default:
			fits = append(fits, h)
		}
	}
	if len(fits) == 0 {
		if len(lacks) == 0 {
			lacks = []string{"hosts.toml has no host"}
		}
		return nil, strings.Join(lacks, "; ")
	}
	return fits, ""
}

// heavy reports whether the card requires a heavy capability.
func (f Fleet) heavy(c tracker.Card) bool {
	return hosts.IsHeavy(c.Requires, f.Heavy)
}

// pick is the host with the most free slots a task like this one may take,
// then the fewest capabilities, then the lowest name, among those with one.
func pick(fits []HostLoad, free map[string]slots, heavy bool) (HostLoad, bool) {
	open := slices.DeleteFunc(slices.Clone(fits), func(h HostLoad) bool { return free[h.Name].left(heavy) < 1 })
	if len(open) == 0 {
		return HostLoad{}, false
	}
	return slices.MinFunc(open, func(a, b HostLoad) int {
		return cmp.Or(
			cmp.Compare(free[b.Name].left(heavy), free[a.Name].left(heavy)),
			cmp.Compare(len(a.Capabilities), len(b.Capabilities)),
			cmp.Compare(a.Name, b.Name))
	}), true
}

func slotKind(heavy bool) string {
	if heavy {
		return "heavy slot"
	}
	return "slot"
}

// slotList says, for each host that could take a task, why it has no slot.
func slotList(fits []HostLoad, free map[string]slots, heavy bool) string {
	var parts []string
	for _, h := range fits {
		if h.Unreachable != "" {
			parts = append(parts, h.Name+" (unreachable: "+h.Unreachable+")")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%d free)", h.Name, free[h.Name].left(heavy)))
	}
	return strings.Join(parts, ", ")
}

// status is a host's reply, or why there is none.
func (f Fleet) status(name string) (hostops.Status, error) {
	h, ok := f.host(name)
	switch {
	case !ok:
		return hostops.Status{}, fmt.Errorf("%q is not in hosts.toml", name)
	case h.Unreachable != "":
		return hostops.Status{}, errors.New(h.Unreachable)
	}
	return h.Status, nil
}

// Quota is the newest reading per harness from hosts that answered.
func (f Fleet) Quota(now time.Time) map[string]session.Quota {
	readings := []map[string]session.Quota{}
	for _, host := range f.Hosts {
		if host.Unreachable == "" {
			readings = append(readings, host.Status.Quota)
		}
	}
	return session.MergeQuota(now, readings...)
}
