package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		bi   *debug.BuildInfo
		want string
	}{
		{"no build info", nil, "engine dev"},
		{
			"working tree build",
			&debug.BuildInfo{GoVersion: "go1.27.2", Main: debug.Module{Version: devel}},
			"engine dev (go1.27.2)",
		},
		{
			"tagged clean build",
			&debug.BuildInfo{
				GoVersion: "go1.27.2",
				Main:      debug.Module{Version: "v0.1.0"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "3f2a9c1d0e8b7a6f"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			"engine v0.1.0 (3f2a9c1, go1.27.2)",
		},
		{
			"dirty short revision",
			&debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			"engine dev (abc-dirty)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromBuildInfo(tt.bi).String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCurrentNamesTheToolchain(t *testing.T) {
	got := Current()
	if !strings.HasPrefix(got.Go, "go") {
		t.Errorf("Current().Go = %q, want a toolchain name", got.Go)
	}
}

func TestCommitAndDirty(t *testing.T) {
	cases := []struct {
		name   string
		info   Info
		commit string
		dirty  bool
	}{
		{"a checkout build", Info{Version: "dev", Revision: "3f2a9c1d"}, "3f2a9c1d", false},
		{"a modified checkout", Info{Version: "dev", Revision: "3f2a9c1d", Modified: true}, "3f2a9c1d", true},
		{"go install module@commit", Info{Version: "v0.0.0-20261025174939-41147c301cf5"}, "41147c301cf5", false},
		{"a dirty pseudo-version", Info{Version: "v0.0.0-20261025174939-41147c301cf5+dirty"}, "41147c301cf5", true},
		{"a tagged release", Info{Version: "v1.2.3"}, "", false},
		{"no build information", Info{Version: "dev"}, "", false},
	}
	for _, c := range cases {
		if got := c.info.Commit(); got != c.commit {
			t.Errorf("%s: commit %q, want %q", c.name, got, c.commit)
		}
		if got := c.info.Dirty(); got != c.dirty {
			t.Errorf("%s: dirty %v, want %v", c.name, got, c.dirty)
		}
	}
}

func TestParseReadsWhatStringPrints(t *testing.T) {
	cases := []struct {
		out    string
		commit string
		dirty  bool
	}{
		{"engine v0.0.0-20261025105230-2a08a0c1f2a2 (2a08a0c, go1.27.2)\n", "2a08a0c", false},
		{"engine dev (2a08a0c-dirty, go1.27.2)", "2a08a0c", true},
		{"engine v0.0.0-20261025174939-41147c301cf5 (go1.27.1)\n", "41147c301cf5", false},
		{"engine v1.2.3 (go1.27.1)", "", false},
		{"engine dev", "", false},
	}
	for _, c := range cases {
		info := Parse(c.out)
		if info.Commit() != c.commit || info.Dirty() != c.dirty {
			t.Errorf("%q: commit %q dirty %v, want %q %v", c.out, info.Commit(), info.Dirty(), c.commit, c.dirty)
		}
	}
	for _, out := range []string{"", "engine: command not found", "bash: engine: not found"} {
		if got := Parse(out); got != (Info{}) {
			t.Errorf("%q parsed as %+v", out, got)
		}
	}
}
