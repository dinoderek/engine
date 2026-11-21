package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramConfigCoexistsWithServer(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		chat      int64
		failure   string
	}{
		{name: "absent"},
		{name: "server only", src: "[server]\nurl='http://server.test'\n"},
		{name: "both", src: "[server]\nurl='http://server.test'\n[telegram]\nchat=-1001234567890", chat: -1001234567890},
		{name: "telegram only", src: "[telegram]\nchat=123", chat: 123},
		{name: "empty telegram", src: "[telegram]\n"},
		{name: "zero", src: "[telegram]\nchat=0", failure: "must not be zero"},
		{name: "string", src: "[telegram]\nchat='123'", failure: "must be an integer"},
		{name: "unknown", src: "[telegram]\nkey=123", failure: "unknown key"},
		{name: "syntax", src: "[telegram", failure: "config.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.src != "" {
				writeFile(t, filepath.Join(dir, "config.toml"), tc.src)
			}
			tg, err := ReadTelegram(dir)
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("%v", err)
				}
				return
			}
			if err != nil || tg.Chat != tc.chat {
				t.Fatalf("%+v %v", tg, err)
			}
			if _, err = ReadServer(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWriteTelegramChatOncePreservesConfig(t *testing.T) {
	for _, src := range []string{"", "[server]\nurl='https://server.test'\n", "[server]\nurl='https://server.test'\n[telegram]\n# empty\n"} {
		t.Run(src, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if src != "" {
				writeFile(t, path, src)
			}
			if err := WriteTelegramChat(dir, 123); err != nil {
				t.Fatal(err)
			}
			got, err := ReadTelegram(dir)
			if err != nil || got.Chat != 123 {
				t.Fatalf("%+v %v", got, err)
			}
			data, _ := os.ReadFile(path)
			if !strings.Contains(string(data), src) {
				t.Fatalf("config changed: %s", data)
			}
			if err = WriteTelegramChat(dir, 456); err == nil {
				t.Fatal("replaced configured chat")
			}
			if err = WriteTelegramChat(dir, 0); err == nil {
				t.Fatal("zero chat")
			}
		})
	}
}
