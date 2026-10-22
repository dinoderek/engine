package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ClaudeConfig is where Claude Code keeps its user state, which includes the
// folders the user trusts: $CLAUDE_CONFIG_DIR/.claude.json when that is set,
// else ~/.claude.json.
func ClaudeConfig(getenv func(string) string, home string) string {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// TrustFolder marks dir as trusted in the Claude Code state file at config,
// so a session started there skips the workspace trust dialog, which would
// otherwise stop an unattended launch (its default answer exits). It sets
// projects[<dir>].hasTrustDialogAccepted, as Claude Code itself does, for dir
// and for its resolved path when that differs. Every other value is kept as
// written (keys are re-sorted and the file re-indented), and the file is
// replaced in one rename; a symlinked file is replaced at its target. Claude Code rewrites the
// file too, without a lock engine could take, so when the file changes
// between the read and the rename, it starts again, a few times at most. A
// file that is not a JSON object is left alone and reported.
func TrustFolder(config, dir string) error { return trustFolder(config, dir, stamp) }

// trustFolder is TrustFolder with the file's stamp read by check, which tests
// replace to play a concurrent writer.
func trustFolder(config, dir string, check func(string) (fileStamp, error)) error {
	if target, err := filepath.EvalSymlinks(config); err == nil {
		config = target
	}
	keys := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		keys = append(keys, real)
	}
	for range trustAttempts {
		data, before, err := readConfig(config)
		if err != nil {
			return err
		}
		changed, out, err := trust(data, keys)
		if err != nil {
			return fmt.Errorf("%s: %w", config, err)
		}
		if !changed {
			return nil
		}
		unchanged := func() bool { now, err := check(config); return err == nil && now == before }
		if done, err := replaceFile(config, out, before.mode, unchanged); done || err != nil {
			return err
		}
	}
	return fmt.Errorf("%s kept changing while engine updated it; trust %s by hand", config, dir)
}

// trustAttempts bounds the retries when another writer keeps changing the file.
const trustAttempts = 3

// fileStamp tells whether a file changed: a missing file has the zero stamp.
type fileStamp struct {
	exists bool
	mod    int64
	size   int64
	mode   fs.FileMode
}

func stamp(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileStamp{mode: 0o600}, nil
	}
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{exists: true, mod: info.ModTime().UnixNano(), size: info.Size(), mode: info.Mode().Perm()}, nil
}

func readConfig(path string) ([]byte, fileStamp, error) {
	st, err := stamp(path)
	if err != nil || !st.exists {
		return []byte("{}"), st, err
	}
	data, err := os.ReadFile(path)
	return data, st, err
}

// trust sets hasTrustDialogAccepted on each key's project, and reports
// whether anything changed.
func trust(data []byte, keys []string) (bool, []byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return false, nil, errors.New("not a JSON object; trust the folder by hand")
	}
	projects := map[string]json.RawMessage{}
	if raw, ok := top["projects"]; ok {
		if err := json.Unmarshal(raw, &projects); err != nil || projects == nil {
			return false, nil, errors.New("projects is not a JSON object; trust the folder by hand")
		}
	}
	changed := false
	for _, key := range keys {
		project := map[string]json.RawMessage{}
		if raw, ok := projects[key]; ok {
			if err := json.Unmarshal(raw, &project); err != nil || project == nil {
				return false, nil, fmt.Errorf("projects[%q] is not a JSON object; trust the folder by hand", key)
			}
		}
		if bytes.Equal(project["hasTrustDialogAccepted"], []byte("true")) {
			continue
		}
		project["hasTrustDialogAccepted"] = json.RawMessage("true")
		enc, err := encode(project, "")
		if err != nil {
			return false, nil, err
		}
		projects[key], changed = enc, true
	}
	if !changed {
		return false, nil, nil
	}
	enc, err := encode(projects, "")
	if err != nil {
		return false, nil, err
	}
	top["projects"] = enc
	out, err := encode(top, "  ")
	return true, out, err
}

// encode is json.Marshal without HTML escaping, so the strings it keeps from
// the file are written back as they were, with indent when it is not empty.
func encode(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// replaceFile writes data beside path and renames it over path, so a reader
// never sees half a file. It renames only if unchanged still holds just
// before, and reports whether it did.
func replaceFile(path string, data []byte, mode fs.FileMode, unchanged func() bool) (bool, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".engine-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if !unchanged() {
		return false, nil
	}
	return true, os.Rename(tmp.Name(), path)
}
