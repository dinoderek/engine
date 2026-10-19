// Package version describes the running engine build from its embedded build
// information.
package version

import (
	"runtime/debug"
	"strings"
)

// devel is the module version Go records for a build from a working tree
// rather than from a tagged module.
const devel = "(devel)"

// Info is what `engine version` reports.
type Info struct {
	Version  string // module version, or "dev" for an untagged build
	Revision string // VCS commit, empty when unknown
	Modified bool   // the working tree had uncommitted changes
	Go       string // toolchain that built the binary
}

// FromBuildInfo extracts Info from Go's build information. A nil bi, as when
// debug.ReadBuildInfo fails, yields an Info with only "dev" set.
func FromBuildInfo(bi *debug.BuildInfo) Info {
	info := Info{Version: "dev"}
	if bi == nil {
		return info
	}
	info.Go = bi.GoVersion
	if v := bi.Main.Version; v != "" && v != devel {
		info.Version = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Revision = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// Current returns the Info of the running binary.
func Current() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		bi = nil
	}
	return FromBuildInfo(bi)
}

// String renders Info as one line, e.g. "engine v0.1.0 (3f2a9c1, go1.27.2)".
func (i Info) String() string {
	var details []string
	if i.Revision != "" {
		rev := i.Revision
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if i.Modified {
			rev += "-dirty"
		}
		details = append(details, rev)
	}
	if i.Go != "" {
		details = append(details, i.Go)
	}
	out := "engine " + i.Version
	if len(details) > 0 {
		out += " (" + strings.Join(details, ", ") + ")"
	}
	return out
}
