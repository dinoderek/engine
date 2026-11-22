package hostops

import (
	"os"
	"path/filepath"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// QuietWait is how long a working session may show no activity before the
// coordinator reports it as hung. Status sends the screen of a working window
// only once it has been quiet this long.
const QuietWait = time.Hour

// lastActivity is when the run's session last showed activity, in epoch
// seconds: its harness transcript's modification time, else its pane log's.
// The transcript is the path the harness's hook events named, which the state
// file keeps. The pane log is only the fallback, for a run that names no
// transcript or whose file is missing: while a turn runs the harness redraws
// its spinner and clock, so a live pane log is always fresh. Zero is unknown.
func lastActivity(r session.Run) int64 {
	paths := []string{}
	if st, ok := session.ReadHarnessState(r.Dir); ok && st.Transcript != "" {
		paths = append(paths, st.Transcript)
	}
	paths = append(paths, filepath.Join(r.Dir, session.PaneLog))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			return info.ModTime().Unix()
		}
	}
	return 0
}

// clock is the host's clock: Now, or the wall clock when it is nil.
func (d Deps) clock() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// quiet reports whether a working window's last activity is QuietWait or
// more before now. Unknown activity is never quiet.
func quiet(activity int64, now time.Time) bool {
	return activity > 0 && now.Sub(time.Unix(activity, 0)) >= QuietWait
}
