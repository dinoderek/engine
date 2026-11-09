package reconcile

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/hostops"
)

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

// Load is a host's load from its status: its free slots, heavy and all, and
// its live sessions by the harness their newest run names.
func Load(name string, harnesses, capabilities []string, st hostops.Status, err error) HostLoad {
	l := HostLoad{Name: name, Harnesses: harnesses, Capabilities: capabilities, Sessions: map[string]int{}}
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

// SignedOut reports whether the host's own check says harness is signed out
// there. A session started with it would stop at its first prompt.
func (h HostLoad) SignedOut(harness string) bool {
	return h.Unreachable == "" && slices.Contains(h.Status.SignedOut, harness)
}

// signedOutText says a harness is signed out on a host, and where the fix is.
func signedOutText(host, harness string) string {
	return fmt.Sprintf("%s is signed out on %s (`engine setup --host %s` names the sign-in)", harness, host, host)
}

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

// Host finds a host by name.
func (f Fleet) Host(name string) (HostLoad, bool) {
	i := slices.IndexFunc(f.Hosts, func(h HostLoad) bool { return h.Name == name })
	if i < 0 {
		return HostLoad{}, false
	}
	return f.Hosts[i], true
}

// LocalFree is how many slots this host has free.
func (f Fleet) LocalFree() int {
	for _, h := range f.Hosts {
		if h.Name == f.Local {
			return h.free().all
		}
	}
	return 0
}

// LiveAnywhere reports whether a host that answered has a live window
// named name.
func (f Fleet) LiveAnywhere(name string) bool {
	return slices.ContainsFunc(f.Hosts, func(h HostLoad) bool { return slices.Contains(h.Live, name) })
}

// Running counts the live sessions of each harness on the hosts that
// answered.
func (f Fleet) Running() map[string]int {
	out := map[string]int{}
	for _, h := range f.Hosts {
		for harness, n := range h.Sessions {
			out[harness] += n
		}
	}
	return out
}

// Want is an item placement is asked to start.
type Want struct {
	ID       string
	Requires []string
	// Harness is the item's pinned harness, or "" to follow the ratio.
	Harness string
	// Invalid, when set, is why the item's harness cannot be read: it waits.
	Invalid error
}

// Placement is where one wanted item starts, or why it waits.
type Placement struct {
	ID      string
	Host    string
	Harness string
	// Wait says why the item cannot start now; it is empty when it starts.
	Wait string
	// Notify is true when no host could take the item even with every slot
	// free.
	Notify bool
}

// Place walks the wanted items in start order and gives each the eligible
// host with the most free slots, then the fewest capabilities, then the
// lowest name. A heavy item counts only a host's free heavy slots. An item
// no host can take now waits, with the reason.
func (f Fleet) Place(wants []Want) []Placement {
	free := map[string]slots{}
	for _, h := range f.Hosts {
		free[h.Name] = h.free()
	}
	running := f.Running()
	var out []Placement
	for _, w := range wants {
		harness, best, why, notify := f.choose(w, free, running)
		if why != "" {
			out = append(out, Placement{ID: w.ID, Wait: why, Notify: notify})
			continue
		}
		free[best.Name] = free[best.Name].take(f.heavy(w))
		running[harness]++
		out = append(out, Placement{ID: w.ID, Host: best.Name, Harness: harness})
	}
	return out
}

// fits returns the hosts that could take the item with this harness if they
// had a free slot. When none could, why says what each host lacks.
func (f Fleet) fits(w Want, harness string) (fits []HostLoad, why string) {
	if _, ok := f.Caps[harness]; !ok {
		return nil, fmt.Sprintf("hosts.toml defines no harness %q (a misspelt tier? tiers are deep, standard and light)", harness)
	}
	var lacks []string
	for _, h := range f.Hosts {
		missing := slices.DeleteFunc(slices.Clone(w.Requires), h.has)
		switch {
		case !slices.Contains(h.Harnesses, harness):
			lacks = append(lacks, fmt.Sprintf("%s does not run %s", h.Name, harness))
		case h.SignedOut(harness):
			lacks = append(lacks, signedOutText(h.Name, harness))
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

// heavy reports whether the item requires a heavy capability.
func (f Fleet) heavy(w Want) bool {
	return slices.ContainsFunc(w.Requires, func(r string) bool { return slices.Contains(f.Heavy, r) })
}

// pick is the host with the most free slots an item like this one may take,
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

// slotList says, for each host that could take an item, why it has no slot.
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
