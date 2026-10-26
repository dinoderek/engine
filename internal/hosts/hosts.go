// Package hosts reads engine's host config: the harnesses and machines of
// ~/.config/engine/hosts.toml, and this machine's name from
// ~/.config/engine/host. It parses the TOML subset the config uses itself,
// because the runtime is standard library only, and reports the first
// violation of the config's rules with its line number.
package hosts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Tier names how much model a task needs. A harness maps every tier to a
// model and an effort level.
type Tier string

// The tiers, strongest first.
const (
	Deep     Tier = "deep"
	Standard Tier = "standard"
	Light    Tier = "light"
)

// Tiers lists every tier.
var Tiers = []Tier{Deep, Standard, Light}

// Capabilities lists what a host may declare and a card may require.
var Capabilities = []string{"macos", "linux", "ios", "vm", "docker"}

// ParseTier returns the tier named s.
func ParseTier(s string) (Tier, error) {
	if t := Tier(s); slices.Contains(Tiers, t) {
		return t, nil
	}
	return "", fmt.Errorf("unknown tier %q: want deep, standard or light", s)
}

// TierModel is the model and effort level a tier runs at.
type TierModel struct {
	Model  string
	Effort string
}

// Harness is one agent CLI engine launches.
type Harness struct {
	// MaxSessions caps the harness across all hosts.
	MaxSessions int
	// Launch is the launch line, with {bin}, {model}, {effort}, {name} and
	// {clone} placeholders.
	Launch string
	Tiers  map[Tier]TierModel
}

// Host is one machine that runs sessions.
type Host struct {
	Name    string
	Address string // <user>@<tailscale name>, passed to ssh as is
	// Harnesses are the names of the harnesses the host runs.
	Harnesses    []string
	RAMGB        int
	ReserveGB    int
	GBPerSession int
	// MaxSessions replaces the count derived from RAM; 0 means derive it.
	MaxSessions int
	// MaxHeavySessions caps the heavy sessions below Slots; 0 means no cap
	// of their own.
	MaxHeavySessions int
	Capabilities     []string
	// Bin is the path of each harness binary, as written: it may start with ~.
	Bin map[string]string
}

// Slots is how many sessions the host runs at once: MaxSessions when set,
// else (RAMGB - ReserveGB) / GBPerSession rounded down, never below 0.
func (h Host) Slots() int {
	if h.MaxSessions > 0 {
		return h.MaxSessions
	}
	if h.GBPerSession < 1 {
		return 0
	}
	return max(0, (h.RAMGB-h.ReserveGB)/h.GBPerSession)
}

// HeavySlots is how many heavy sessions the host runs at once:
// MaxHeavySessions when set, else all its slots.
func (h Host) HeavySlots() int {
	if h.MaxHeavySessions > 0 {
		return h.MaxHeavySessions
	}
	return h.Slots()
}

// Has reports whether the host declares the capability.
func (h Host) Has(capability string) bool { return slices.Contains(h.Capabilities, capability) }

// Runs reports whether the host runs the harness.
func (h Host) Runs(harness string) bool { return slices.Contains(h.Harnesses, harness) }

// Config is a validated hosts.toml.
type Config struct {
	DefaultHarness string
	GBPerSession   int
	ReserveGB      int
	// HeavyCapabilities make a task heavy when its card requires one.
	HeavyCapabilities []string
	Harnesses         map[string]Harness
	Hosts             map[string]Host
}

// HostNames lists the configured hosts, sorted.
func (c *Config) HostNames() []string {
	names := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Heavy reports whether a task that requires these capabilities is heavy.
func (c *Config) Heavy(requires []string) bool { return IsHeavy(requires, c.HeavyCapabilities) }

// IsHeavy reports whether requires names one of the heavy capabilities.
func IsHeavy(requires, heavy []string) bool {
	return slices.ContainsFunc(requires, func(r string) bool { return slices.Contains(heavy, r) })
}

// Model returns the model and effort the harness runs a tier at.
func (c *Config) Model(harness string, tier Tier) (TierModel, error) {
	h, ok := c.Harnesses[harness]
	if !ok {
		return TierModel{}, fmt.Errorf("harness %q is not in hosts.toml", harness)
	}
	m, ok := h.Tiers[tier]
	if !ok {
		return TierModel{}, fmt.Errorf("harness %q has no tier %q", harness, tier)
	}
	return m, nil
}

// Local returns the host this machine is, by the name in its host file.
func (c *Config) Local(name string) (Host, error) {
	h, ok := c.Hosts[name]
	if !ok {
		return Host{}, fmt.Errorf("host %q is not in hosts.toml (hosts: %s)", name, strings.Join(c.HostNames(), ", "))
	}
	return h, nil
}

// Parse validates the text of a hosts.toml. The error is an *Error.
func Parse(src string) (*Config, error) {
	root, err := parse(src)
	if err != nil {
		return nil, err
	}
	c, bad := decode(root)
	if bad != nil {
		return nil, bad
	}
	return c, nil
}

// Load reads and validates the hosts.toml at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read host config: %w", err)
	}
	c, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// ReadHostName reads this machine's name from its host file: one line.
func ReadHostName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read host file: %w", err)
	}
	name := strings.TrimSpace(string(data))
	switch {
	case name == "":
		return "", errors.New(path + " is empty: it holds this host's name")
	case strings.ContainsAny(name, " \t\r\n"):
		return "", errors.New(path + " must hold one host name on one line")
	}
	return name, nil
}

// ConfigDir is where the host config lives under home.
func ConfigDir(home string) string { return filepath.Join(home, ".config", "engine") }

// CodexHomeDir is engine's own Codex home, under home: the config, login link,
// plugin and app-server daemon that engine setup prepares for engine's Codex
// sessions to join, apart from the operator's own Codex home.
const CodexHomeDir = ".local/share/engine/codex"

// Open loads hosts.toml and the host file from dir, and returns the config
// with the host this machine is.
func Open(dir string) (*Config, Host, error) {
	c, err := Load(filepath.Join(dir, "hosts.toml"))
	if err != nil {
		return nil, Host{}, err
	}
	name, err := ReadHostName(filepath.Join(dir, "host"))
	if err != nil {
		return nil, Host{}, err
	}
	h, err := c.Local(name)
	if err != nil {
		return nil, Host{}, err
	}
	return c, h, nil
}
