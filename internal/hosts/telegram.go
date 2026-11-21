package hosts

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Telegram is the optional [telegram] table in the operator's config.toml.
type Telegram struct {
	Chat         int64
	DashboardURL string
}

func dashboardOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u != nil && (u.Scheme == "http" || u.Scheme == "https") &&
		u.Hostname() != "" && u.User == nil && (u.Path == "" || u.Path == "/") &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "#")
}

// ReadTelegram leaves an absent configuration disabled.
func ReadTelegram(dir string) (Telegram, error) {
	path := filepath.Join(dir, "config.toml")
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Telegram{}, nil
	}
	if err != nil {
		return Telegram{}, err
	}
	_, tg, err := parseOperatorConfig(string(src))
	if err != nil {
		return Telegram{}, fmt.Errorf("%s: %w", path, err)
	}
	return tg, nil
}

// WriteTelegramChat adds a chat once, preserving other config and comments.
// Setup refuses to poll when a chat is already configured.
func WriteTelegramChat(dir string, chat int64) error {
	if chat == 0 {
		return errors.New("Telegram chat must not be zero")
	}
	path := filepath.Join(dir, "config.toml")
	src, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root := &node{entries: map[string]*node{}}
	if len(src) > 0 {
		if _, _, err = parseOperatorConfig(string(src)); err != nil {
			return err
		}
		root, err = parse(string(src))
		if err != nil {
			return err
		}
	}
	if table := root.entries["telegram"]; table != nil && table.entries["chat"] != nil {
		return errors.New("Telegram chat is already configured")
	}
	text := string(src)
	if root.entries["telegram"] == nil {
		text += "\n[telegram]\n"
		text += fmt.Sprintf("chat = %d\n", chat)
	} else {
		// A dotted assignment avoids depending on where [telegram] appears.
		text = fmt.Sprintf("telegram.chat = %d\n", chat) + text
	}
	if _, _, err = parseOperatorConfig(text); err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".telegram-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
