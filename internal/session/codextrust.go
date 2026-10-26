package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TrustCodexFolder trusts dir, and its resolved path when that differs, in
// the Codex config.toml at config: one `[projects."<path>"]` table each,
// appended after what is there. A session that joins a remote-control
// daemon takes its folder trust only from the daemon's config.toml, one
// entry per folder. A table already there for the path is replaced. Every
// other line is kept as written, and the file is replaced in one rename; a
// symlinked file is replaced at its target.
// Codex and engine setup write the file too, without a lock engine could
// take, so when it changes between the read and the rename, it starts again,
// a few times at most.
func TrustCodexFolder(config, dir string) error {
	return editCodexConfig(config, dir, stamp, func(text string, keys []string) string {
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

// UntrustCodexFolder removes what TrustCodexFolder added for dir. A missing
// file is left missing.
func UntrustCodexFolder(config, dir string) error {
	return editCodexConfig(config, dir, stamp, withoutProjects)
}

// editCodexConfig rewrites the file at config with edit, when that changes
// it, retrying while another writer changes it: check reads the file's stamp
// just before the rename, and tests replace it to play that writer.
func editCodexConfig(config, dir string, check func(string) (fileStamp, error), edit func(text string, keys []string) string) error {
	if target, err := filepath.EvalSymlinks(config); err == nil {
		config = target
	}
	keys := trustKeys(dir)
	for range trustAttempts {
		before, err := stamp(config)
		if err != nil {
			return err
		}
		var data []byte
		if before.exists {
			if data, err = os.ReadFile(config); err != nil {
				return err
			}
		}
		out := edit(string(data), keys)
		if out == string(data) {
			return nil
		}
		unchanged := func() bool { now, err := check(config); return err == nil && now == before }
		if done, err := replaceFile(config, []byte(strings.TrimSuffix(out, "\n")), before.mode, unchanged); done || err != nil {
			return err
		}
	}
	return fmt.Errorf("%s kept changing while engine updated it; edit the trust of %s by hand", config, dir)
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
