package hostops

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// paneTailBytes bounds how much of a pane log is read to find its last lines.
const paneTailBytes = 8 << 10

// paneTail is the tail of the pane log in run directory dir, as of the
// host's clock.
func (d Deps) paneTail(dir string) string {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	return paneTailOf(filepath.Join(dir, session.PaneLog), now(), PaneTailAge)
}

// paneTailOf is the last PaneTailLines lines of the pane log at path, or empty
// when it is missing, unreadable or was last written before now less age. The
// log holds the window's raw output, so a partial first line is dropped.
func paneTailOf(path string, now time.Time, age time.Duration) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || now.Sub(info.ModTime()) > age {
		return ""
	}
	cut := info.Size() > paneTailBytes
	if cut {
		if _, err := f.Seek(-paneTailBytes, io.SeekEnd); err != nil {
			return ""
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if cut && len(lines) > 1 {
		lines = lines[1:]
	}
	return strings.Join(lines[max(len(lines)-PaneTailLines, 0):], "\n")
}
