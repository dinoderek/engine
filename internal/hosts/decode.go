package hosts

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// validator collects every violation, so the one reported is the first in the
// file whatever order the rules ran in.
type validator struct{ errs []*Error }

func (v *validator) fail(line int, format string, args ...any) {
	v.errs = append(v.errs, &Error{Line: line, Msg: fmt.Sprintf(format, args...)})
}

func (v *validator) first() *Error {
	if len(v.errs) == 0 {
		return nil
	}
	first := v.errs[0]
	for _, e := range v.errs[1:] {
		if e.Line < first.Line {
			first = e
		}
	}
	return first
}

// reader reads typed keys from one table and flags the keys nothing read.
type reader struct {
	v     *validator
	n     *node
	where string
	used  map[string]bool
}

func (v *validator) reader(n *node, where string) *reader {
	return &reader{v: v, n: n, where: where, used: map[string]bool{}}
}

// lookup returns the key's value, or nil after recording why there is none.
func (r *reader) lookup(key string, k kind, required bool) *node {
	r.used[key] = true
	n := r.n.entries[key]
	switch {
	case n == nil && required:
		r.v.fail(r.n.line, "%s: missing %s", r.where, key)
	case n == nil:
	case n.kind != k:
		r.v.fail(n.line, "%s: %s must be %s", r.where, key, k)
	default:
		return n
	}
	return nil
}

// text reads a non-empty string.
func (r *reader) text(key string, required bool) (string, *node) {
	n := r.lookup(key, kString, required)
	if n == nil {
		return "", nil
	}
	if n.str == "" {
		r.v.fail(n.line, "%s: %s must not be empty", r.where, key)
	}
	return n.str, n
}

// number reads an integer of at least lo, and says whether the key was set.
func (r *reader) number(key string, required bool, lo int) (int, bool) {
	n := r.lookup(key, kInt, required)
	if n == nil {
		return 0, false
	}
	if n.num < lo {
		r.v.fail(n.line, "%s: %s must be at least %d", r.where, key, lo)
	}
	return n.num, true
}

func (r *reader) list(key string, required bool) ([]string, *node) {
	n := r.lookup(key, kArray, required)
	if n == nil {
		return nil, nil
	}
	return n.items, n
}

func (r *reader) table(key string, required bool) *node {
	return r.lookup(key, kTable, required)
}

// finish flags every key that was never read.
func (r *reader) finish() {
	for _, key := range r.n.order {
		if !r.used[key] {
			r.v.fail(r.n.entries[key].line, "%s: unknown key %s", r.where, key)
		}
	}
}

// children reads each entry of the table as a table of its own.
func (v *validator) children(n *node, where string, each func(name string, child *node)) {
	if n == nil {
		return
	}
	for _, name := range n.order {
		child := n.entries[name]
		if child.kind != kTable {
			v.fail(child.line, "%s.%s must be a table", where, name)
			continue
		}
		each(name, child)
	}
}

// decode validates the parsed file against the rules of the host config and
// builds the typed Config from it.
func decode(root *node) (*Config, *Error) {
	v := &validator{}
	c := &Config{Harnesses: map[string]Harness{}, Hosts: map[string]Host{}}
	r := v.reader(root, "top level")
	var defaultAt *node
	c.DefaultHarness, defaultAt = r.text("default_harness", true)
	c.GBPerSession, _ = r.number("gb_per_session", true, 1)
	c.ReserveGB, _ = r.number("reserve_gb", true, 0)
	harnesses := r.table("harness", true)
	v.children(harnesses, "harness", func(name string, n *node) { c.Harnesses[name] = v.harness(name, n) })
	hosts := r.table("host", true)
	v.children(hosts, "host", func(name string, n *node) { c.Hosts[name] = v.host(c, name, n) })
	r.finish()
	if hosts != nil && len(hosts.order) == 0 {
		v.fail(hosts.line, "top level: no [host.<name>] is defined")
	}
	if harnesses != nil && len(harnesses.order) == 0 {
		v.fail(harnesses.line, "top level: no [harness.<name>] is defined")
	}
	if defaultAt != nil && len(c.Harnesses) > 0 {
		if _, ok := c.Harnesses[c.DefaultHarness]; !ok {
			v.fail(defaultAt.line, "top level: default_harness %q is not defined under [harness]", c.DefaultHarness)
		}
	}
	return c, v.first()
}

// launchPlaceholders are the {words} a launch line may use.
var launchPlaceholders = []string{"bin", "model", "effort", "name", "clone"}

var placeholder = regexp.MustCompile(`\{([A-Za-z_]+)\}`)

func (v *validator) harness(name string, n *node) Harness {
	where := "harness." + name
	r := v.reader(n, where)
	h := Harness{Tiers: map[Tier]TierModel{}}
	h.MaxSessions, _ = r.number("max_sessions", true, 1)
	launch, at := r.text("launch", true)
	if at != nil {
		v.checkLaunch(where, launch, at.line)
		h.Launch = launch
	}
	if tiers := r.table("tiers", true); tiers != nil {
		tr := v.reader(tiers, where+".tiers")
		for _, tier := range Tiers {
			h.Tiers[tier] = v.tier(tr, where+".tiers."+string(tier), tier)
		}
		tr.finish()
	}
	r.finish()
	return h
}

func (v *validator) tier(tiers *reader, where string, tier Tier) TierModel {
	n := tiers.table(string(tier), true)
	if n == nil {
		return TierModel{}
	}
	r := v.reader(n, where)
	var m TierModel
	m.Model, _ = r.text("model", true)
	m.Effort, _ = r.text("effort", true)
	r.finish()
	return m
}

func (v *validator) checkLaunch(where, launch string, line int) {
	if launch != "{bin}" && !strings.HasPrefix(launch, "{bin} ") {
		v.fail(line, "%s: launch must start with {bin}, so it never runs whichever install is first on PATH", where)
	}
	if !strings.Contains(launch, "{model}") {
		v.fail(line, "%s: launch must contain {model}", where)
	}
	for _, m := range placeholder.FindAllStringSubmatch(launch, -1) {
		if !slices.Contains(launchPlaceholders, m[1]) {
			v.fail(line, "%s: launch uses unknown placeholder {%s}; known: %s", where, m[1], strings.Join(launchPlaceholders, ", "))
		}
	}
}

func (v *validator) host(c *Config, name string, n *node) Host {
	where := "host." + name
	r := v.reader(n, where)
	h := Host{Name: name, ReserveGB: c.ReserveGB, GBPerSession: c.GBPerSession}
	if addr, at := r.text("address", true); at != nil {
		h.Address = addr
		if user, machine, ok := strings.Cut(addr, "@"); !ok || user == "" || machine == "" || strings.Contains(machine, "@") || strings.HasPrefix(addr, "-") {
			v.fail(at.line, "%s: address %q must be <user>@<tailscale name>", where, addr)
		}
	}
	v.hostHarnesses(c, r, &h)
	v.hostLimits(r, &h)
	v.hostCapabilities(r, &h)
	v.hostBin(r, &h)
	r.finish()
	return h
}

func (v *validator) hostHarnesses(c *Config, r *reader, h *Host) {
	list, at := r.list("harnesses", true)
	if at == nil {
		return
	}
	if len(list) == 0 {
		v.fail(at.line, "%s: harnesses must list at least one harness", r.where)
	}
	for i, name := range list {
		if _, ok := c.Harnesses[name]; !ok {
			v.fail(at.line, "%s: harnesses lists %q, which is not defined under [harness]", r.where, name)
		}
		if slices.Contains(list[:i], name) {
			v.fail(at.line, "%s: harnesses lists %q twice", r.where, name)
		}
	}
	h.Harnesses = list
}

func (v *validator) hostLimits(r *reader, h *Host) {
	h.RAMGB, _ = r.number("ram_gb", true, 1)
	if n, ok := r.number("reserve_gb", false, 0); ok {
		h.ReserveGB = n
	}
	if n, ok := r.number("gb_per_session", false, 1); ok {
		h.GBPerSession = n
	}
	h.MaxSessions, _ = r.number("max_sessions", false, 1)
}

func (v *validator) hostCapabilities(r *reader, h *Host) {
	list, at := r.list("capabilities", false)
	if at == nil {
		return
	}
	for i, name := range list {
		if !slices.Contains(Capabilities, name) {
			v.fail(at.line, "%s: unknown capability %q; known: %s", r.where, name, strings.Join(Capabilities, ", "))
		}
		if slices.Contains(list[:i], name) {
			v.fail(at.line, "%s: capabilities lists %q twice", r.where, name)
		}
	}
	h.Capabilities = list
}

// hostBin reads the path of each harness binary: every harness the host runs
// has one, and no other does.
func (v *validator) hostBin(r *reader, h *Host) {
	bin := r.table("bin", true)
	if bin == nil || len(h.Harnesses) == 0 {
		return
	}
	br := v.reader(bin, r.where+".bin")
	h.Bin = map[string]string{}
	for _, harness := range h.Harnesses {
		if path, _ := br.text(harness, true); path != "" {
			h.Bin[harness] = path
		}
	}
	br.finish()
}
