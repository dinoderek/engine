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
