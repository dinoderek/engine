package hosts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadServerURL reads the operator CLI's server origin from config.toml.
// An absent file leaves the choice of default to the client.
func ReadServerURL(dir string) (string, error) {
	path := filepath.Join(dir, "config.toml")
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	url, err := serverURL(string(src))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return url, nil
}

func serverURL(src string) (string, error) {
	root, err := parse(src)
	if err != nil {
		return "", err
	}
	v := &validator{}
	r := v.reader(root, "top level")
	table := r.table("server", true)
	r.finish()
	url := ""
	if table != nil {
		s := v.reader(table, "server")
		url, _ = s.text("url", true)
		s.finish()
	}
	if bad := v.first(); bad != nil {
		return "", bad
	}
	return url, nil
}
