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
// and for its resolved path when that differs. Every other key is kept byte
// for byte, and the file is replaced in one rename. A file that is not a JSON
// object is left alone and reported.
func TrustFolder(config, dir string) error {
	keys := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		keys = append(keys, real)
	}
	data, mode, err := readConfig(config)
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
	return replaceFile(config, out, mode)
}

func readConfig(path string) ([]byte, fs.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("{}"), 0o600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(path)
	return data, info.Mode().Perm(), err
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
// never sees half a file.
func replaceFile(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".engine-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
