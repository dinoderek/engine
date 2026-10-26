package hosts

import (
	"strings"
	"testing"
)

func TestLaunchLineExpandsEveryPlaceholder(t *testing.T) {
	c, err := Parse(docExample(t))
	if err != nil {
		t.Fatal(err)
	}
	mac := c.Hosts["mac"]
	got, err := c.LaunchLine(mac, Launch{Harness: "claude", Tier: Deep, Name: "boga3-412", Clone: "/c/o/boga3", Home: "/home/me"})
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/me/.local/bin/claude --permission-mode auto --model claude-opus-5-5 --effort high -n boga3-412 --remote-control boga3-412"
	if got != want {
		t.Errorf("claude:\n got %s\nwant %s", got, want)
	}
	got, err = c.LaunchLine(mac, Launch{Harness: "codex", Tier: Light, Name: "r-1", Clone: "/c/o/r", Home: "/home/me"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "/home/me/.local/bin/codex ") || !strings.Contains(got, `["/c/o/r"]`) ||
		!strings.Contains(got, "-m gpt-6-luna -c model_reasoning_effort=medium") || strings.Contains(got, "{") {
		t.Errorf("codex: %s", got)
	}
}

func TestLaunchLineQuotesValues(t *testing.T) {
	c, err := Parse(strings.Replace(base, `"~/bin/claude"`, `"/opt/my tools/claude"`, 1))
	if err != nil {
		t.Fatal(err)
	}
	c.Harnesses["claude"].Tiers[Deep] = TierModel{Model: "opus; rm -rf ~", Effort: "high"}
	got, err := c.LaunchLine(c.Hosts["mac"], Launch{Harness: "claude", Tier: Deep, Name: "r-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := `'/opt/my tools/claude' --model 'opus; rm -rf ~' --effort high -n r-1`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestLaunchLineTakesTheCallersModel(t *testing.T) {
	c, err := Parse(docExample(t))
	if err != nil {
		t.Fatal(err)
	}
	// The tier is ignored, even an unknown one: the caller already chose.
	l := Launch{Harness: "claude", Tier: "huge", Name: "r-1", Home: "/home/me", Model: TierModel{Model: "opus-y", Effort: "max"}}
	got, err := c.LaunchLine(c.Hosts["mac"], l)
	if err != nil || !strings.Contains(got, "--model opus-y --effort max ") {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestLaunchLineFailures(t *testing.T) {
	c, err := Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	mac := c.Hosts["mac"]
	cases := []struct {
		name string
		host Host
		l    Launch
		want string
	}{
		{"unknown harness", mac, Launch{Harness: "codex", Tier: Deep, Home: "/h"}, `harness "codex" is not in hosts.toml`},
		{"host does not run it", Host{Name: "box"}, Launch{Harness: "claude", Tier: Deep, Home: "/h"}, "host box does not run"},
		{"unknown tier", mac, Launch{Harness: "claude", Tier: "huge", Home: "/h"}, `has no tier "huge"`},
		{"no home for ~", mac, Launch{Harness: "claude", Tier: Deep}, "no home directory"},
	}
	for _, tc := range cases {
		if _, err := c.LaunchLine(tc.host, tc.l); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

// {clone} sits inside quotes in Codex's launch line, so a clone path that is
// not a plain word fails; a launch line without {clone} takes any.
func TestLaunchLineNeedsAPlainClone(t *testing.T) {
	c, err := Parse(docExample(t))
	if err != nil {
		t.Fatal(err)
	}
	mac := c.Hosts["mac"]
	for _, clone := range []string{"/home/my user/r", `/c/"]x`, "/c/$(x)"} {
		_, err := c.LaunchLine(mac, Launch{Harness: "codex", Tier: Light, Name: "r-1", Clone: clone, Home: "/home/me"})
		if err == nil || !strings.Contains(err.Error(), "not a plain word") {
			t.Errorf("codex, clone %q: %v", clone, err)
		}
	}
	if _, err := c.LaunchLine(mac, Launch{Harness: "claude", Tier: Light, Name: "r-1", Clone: "/home/my user/r", Home: "/home/me"}); err != nil {
		t.Errorf("claude: %v", err)
	}
}

func TestBinPath(t *testing.T) {
	h := Host{Name: "mac", Bin: map[string]string{"claude": "~/bin/claude", "codex": "/usr/bin/codex", "x": "bin/x"}}
	cases := map[string]string{"claude": "/home/me/bin/claude", "codex": "/usr/bin/codex"}
	for harness, want := range cases {
		if got, err := h.BinPath(harness, "/home/me"); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", harness, got, err, want)
		}
	}
	if _, err := h.BinPath("x", "/home/me"); err == nil || !strings.Contains(err.Error(), "want an absolute path") {
		t.Errorf("relative bin: %v", err)
	}
	if _, err := h.BinPath("gemini", "/home/me"); err == nil || !strings.Contains(err.Error(), "no bin") {
		t.Errorf("missing bin: %v", err)
	}
}

func TestShellWord(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5":    "claude-opus-5-5",
		"/a/b.c":             "/a/b.c",
		"a b":                "'a b'",
		"it's":               `'it'\''s'`,
		"$(x)":               "'$(x)'",
		"":                   "''",
		"Execute o/r with x": "'Execute o/r with x'",
	} {
		if got := ShellWord(in); got != want {
			t.Errorf("ShellWord(%q) = %s, want %s", in, got, want)
		}
	}
}
