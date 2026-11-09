package codex

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/session"
)

// TrustFolder trusts dir, and its resolved path when that differs, in the
// Codex config.toml at config: one `[projects."<path>"]` table each, appended
// after what is there. A session that joins a remote-control daemon takes its
// folder trust only from the daemon's config.toml, one entry per folder. A
// table already there for the path is replaced. Every other line is kept as
// written. Codex and engine setup write the file too, so the edit retries as
// session.EditFile says.
func TrustFolder(config, dir string) error {
	return editConfig(config, dir, nil, func(text string, keys []string) string {
		text = strings.TrimRight(withoutProjects(text, keys), "\n")
		for _, k := range keys {
			if text != "" {
				text += "\n\n"
			}
			text += "[projects." + TOMLString(k) + "]\ntrust_level = \"trusted\""
		}
		return text + "\n"
	})
}

// UntrustFolder removes what TrustFolder added for dir. A missing file is
// left missing.
func UntrustFolder(config, dir string) error {
	return editConfig(config, dir, nil, withoutProjects)
}

// editConfig rewrites the file at config with edit, when that changes it.
// check reads the file's stamp just before the rename; tests replace it to
// play another writer.
func editConfig(config, dir string, check func(string) (session.Stamp, error), edit func(text string, keys []string) string) error {
	keys := session.TrustKeys(dir)
	err := session.EditFile(config, check, func(data []byte) ([]byte, error) {
		out := edit(string(data), keys)
		if out == string(data) {
			return nil, nil
		}
		return []byte(out), nil
	})
	if errors.Is(err, session.ErrKeptChanging) {
		return fmt.Errorf("%w; edit the trust of %s by hand", err, dir)
	}
	return err
}

// withoutProjects is text without the `[projects."<key>"]` table of each
// key: its header and every line up to the next table.
func withoutProjects(text string, keys []string) string {
	headers := map[string]bool{}
	for _, k := range keys {
		headers["[projects."+TOMLString(k)+"]"] = true
		headers["[projects.'"+k+"']"] = true
	}
	var out strings.Builder
	skipping, removed := false, false
	for _, line := range strings.SplitAfter(text, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "[") {
			skipping = headers[t]
			removed = removed || skipping
		}
		if !skipping {
			out.WriteString(line)
		}
	}
	if !removed {
		return text
	}
	if trimmed := strings.TrimRight(out.String(), "\n"); trimmed != "" {
		return trimmed + "\n"
	}
	return ""
}
