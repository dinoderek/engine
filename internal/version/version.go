// Package version describes the running engine build from its embedded build
// information.
package version

import (
	"regexp"
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
	bi, _ := debug.ReadBuildInfo() // nil when the binary has none
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

// pseudoRe is the commit a pseudo-version ends in: the version of a build made
// by `go install module@commit`, which records no VCS settings.
var pseudoRe = regexp.MustCompile(`-\d{14}-([0-9a-f]{12})(\+dirty)?$`)

// Commit is the commit the build was made from: its VCS revision, else the one
// its pseudo-version ends in. It is empty when the build records neither.
func (i Info) Commit() string {
	if i.Revision != "" {
		return i.Revision
	}
	if m := pseudoRe.FindStringSubmatch(i.Version); m != nil {
		return m[1]
	}
	return ""
}

// Dirty reports whether the build was made from a modified working tree.
func (i Info) Dirty() bool {
	return i.Modified || strings.HasSuffix(i.Version, "+dirty")
}

// stringRe reads the first line of Info.String: "engine <version> (<details>)".
var stringRe = regexp.MustCompile(`^engine (\S+)(?: \(([^)]*)\))?`)

// shortRevRe is the revision String prints, with "-dirty" when modified.
var shortRevRe = regexp.MustCompile(`^([0-9a-f]{7,40})(-dirty)?$`)

// Parse reads what String printed, which another engine's `engine version`
// prints. A line that is not engine's yields an Info with no version.
func Parse(output string) Info {
	line, _, _ := strings.Cut(output, "\n")
	m := stringRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Info{}
	}
	info := Info{Version: m[1]}
	for _, detail := range strings.Split(m[2], ", ") {
		if r := shortRevRe.FindStringSubmatch(detail); r != nil {
			info.Revision, info.Modified = r[1], r[2] != ""
		}
	}
	return info
}
