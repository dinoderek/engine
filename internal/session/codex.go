package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CodexHome is where Codex keeps its config: $CODEX_HOME when that is set,
// else ~/.codex.
func CodexHome(getenv func(string) string, home string) string {
	if dir := getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// CodexProfile is the name of engine's Codex profile for the session name:
// `codex -p <profile>` layers <codex home>/<profile>.config.toml on the
// user's own config.
func CodexProfile(name string) string { return "engine-" + name }

// CodexProfilePath is the file of the profile in codexHome.
func CodexProfilePath(codexHome, profile string) string {
	return filepath.Join(codexHome, profile+".config.toml")
}

// WriteCodexProfile writes engine's profile for a session in codexHome: it
// trusts dir, and its resolved path when that differs, so a session started
// there skips Codex's folder-trust prompt, and makes clone a writable root,
// because a worktree's git metadata lives in its clone. The user's
// config.toml is never touched. The file is replaced in one rename.
func WriteCodexProfile(codexHome, profile, dir, clone string) error {
	if !validPart(profile) {
		return fmt.Errorf("codex profile %q: use letters, digits, _, . and -", profile)
	}
	var b strings.Builder
	b.WriteString("# engine's profile for one session: it trusts the session's folder.\n")
	b.WriteString("# engine host release deletes it.\n")
	for _, k := range trustKeys(dir) {
		fmt.Fprintf(&b, "\n[projects.%s]\ntrust_level = \"trusted\"\n", TOMLString(k))
	}
	fmt.Fprintf(&b, "\n[sandbox_workspace_write]\nwritable_roots = [%s]\n", TOMLString(clone))
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	_, err := replaceFile(CodexProfilePath(codexHome, profile), []byte(strings.TrimSuffix(b.String(), "\n")), 0o600, func() bool { return true })
	return err
}

// trustKeys are the paths that trust dir: dir, and its resolved path when
// that differs.
func trustKeys(dir string) []string {
	keys := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		keys = append(keys, real)
	}
	return keys
}

// RemoveCodexProfile deletes engine's profile from codexHome, and reports
// whether there was one.
func RemoveCodexProfile(codexHome, profile string) (bool, error) {
	err := os.Remove(CodexProfilePath(codexHome, profile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// TOMLString quotes s as a TOML basic string.
func TOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
