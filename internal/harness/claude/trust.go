package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dinoderek/engine/internal/session"
)

// Config is where Claude Code keeps its user state, which includes the
// folders the user trusts: $CLAUDE_CONFIG_DIR/.claude.json when that is set,
// else ~/.claude.json.
func Config(getenv func(string) string, home string) string {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// Marketplace is the name of the plugin marketplace engine publishes
// (.claude-plugin/marketplace.json).
const Marketplace = "engine"

// PluginDir is where Claude Code keeps the plugins it installed from engine's
// marketplace: under $CLAUDE_CONFIG_DIR when that is set, else under ~/.claude.
func PluginDir(getenv func(string) string, home string) string {
	dir := getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "plugins", "cache", Marketplace)
}

// TrustFolder marks dir as trusted in the Claude Code state file at config,
// so a session started there skips the workspace trust dialog, which would
// otherwise stop an unattended launch (its default answer exits). It sets
// projects[<dir>].hasTrustDialogAccepted, as Claude Code itself does, for dir
// and for its resolved path when that differs. Every other value is kept as
// written (keys are re-sorted and the file re-indented). Claude Code rewrites
// the file too, so the edit retries as session.EditFile says. A file that is
// not a JSON object is left alone and reported.
func TrustFolder(config, dir string) error { return trustFolder(config, dir, nil) }

// trustFolder is TrustFolder with the file's stamp read by check, which tests
// replace to play a concurrent writer.
func trustFolder(config, dir string, check func(string) (session.Stamp, error)) error {
	keys := session.TrustKeys(dir)
	err := session.EditFile(config, check, func(data []byte) ([]byte, error) {
		if data == nil {
			data = []byte("{}")
		}
		return trust(data, keys)
	})
	if errors.Is(err, session.ErrKeptChanging) {
		return fmt.Errorf("%w; trust %s by hand", err, dir)
	}
	return err
}

// trust sets hasTrustDialogAccepted on each key's project, or returns nil
// when every key already has it.
func trust(data []byte, keys []string) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, errors.New("not a JSON object; trust the folder by hand")
	}
	projects := map[string]json.RawMessage{}
	if raw, ok := top["projects"]; ok {
		if err := json.Unmarshal(raw, &projects); err != nil || projects == nil {
			return nil, errors.New("projects is not a JSON object; trust the folder by hand")
		}
	}
	changed := false
	for _, key := range keys {
		project := map[string]json.RawMessage{}
		if raw, ok := projects[key]; ok {
			if err := json.Unmarshal(raw, &project); err != nil || project == nil {
				return nil, fmt.Errorf("projects[%q] is not a JSON object; trust the folder by hand", key)
			}
		}
		if bytes.Equal(project["hasTrustDialogAccepted"], []byte("true")) {
			continue
		}
		project["hasTrustDialogAccepted"] = json.RawMessage("true")
		enc, err := encode(project, "")
		if err != nil {
			return nil, err
		}
		projects[key], changed = enc, true
	}
	if !changed {
		return nil, nil
	}
	enc, err := encode(projects, "")
	if err != nil {
		return nil, err
	}
	top["projects"] = enc
	return encode(top, "  ")
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
