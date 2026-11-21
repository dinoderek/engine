package hosts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ServerPort is the port the control server listens on when config.toml
// names its host.
const ServerPort = 7331

// Server is config.toml's [server] table: the host the control server runs
// on, or an explicit origin. Exactly one is set.
type Server struct {
	Host string
	URL  string
}

// ReadServer reads config.toml's [server] table. An absent file is an empty
// Server, which leaves the choice of default to the caller.
func ReadServer(dir string) (Server, error) {
	path := filepath.Join(dir, "config.toml")
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Server{}, nil
	}
	if err != nil {
		return Server{}, err
	}
	s, err := parseServer(string(src))
	if err != nil {
		return Server{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ServerOrigin is the origin operator CLIs use: config.toml's url, else the
// origin of its host as hosts.toml defines it, else empty.
func ServerOrigin(dir string) (string, error) {
	s, err := ReadServer(dir)
	if err != nil || s.Host == "" {
		return s.URL, err
	}
	c, err := Load(filepath.Join(dir, "hosts.toml"))
	if err != nil {
		return "", err
	}
	h, ok := c.Hosts[s.Host]
	if !ok {
		return "", fmt.Errorf("config.toml: server host %q is not in hosts.toml", s.Host)
	}
	_, origin := ServerAddress(h)
	return origin, nil
}

// ServerAddress is where a control server on h listens and the origin it
// advertises: the host's Tailscale name, from its address, on ServerPort.
func ServerAddress(h Host) (listen, origin string) {
	name := h.Address
	if i := strings.LastIndex(name, "@"); i >= 0 {
		name = name[i+1:]
	}
	listen = name + ":" + strconv.Itoa(ServerPort)
	return listen, "http://" + listen
}

func parseServer(src string) (Server, error) {
	s, _, err := parseOperatorConfig(src)
	return s, err
}

func parseOperatorConfig(src string) (Server, Telegram, error) {
	root, err := parse(src)
	if err != nil {
		return Server{}, Telegram{}, err
	}
	v := &validator{}
	r := v.reader(root, "top level")
	telegram := r.table("telegram", false)
	table := r.table("server", telegram == nil)
	r.finish()
	var s Server
	if table != nil {
		t := v.reader(table, "server")
		s.Host, _ = t.text("host", false)
		s.URL, _ = t.text("url", false)
		t.finish()
		if v.first() == nil && (s.Host == "") == (s.URL == "") {
			v.fail(table.pos, "[server] must set exactly one of host and url")
		}
	}
	var tg Telegram
	if telegram != nil {
		t := v.reader(telegram, "telegram")
		if chat := t.lookup("chat", kInt, false); chat != nil {
			tg.Chat = int64(chat.num)
			if tg.Chat == 0 {
				v.fail(chat.pos, "telegram: chat must not be zero")
			}
		}
		t.finish()
	}
	if bad := v.first(); bad != nil {
		return Server{}, Telegram{}, bad
	}
	return s, tg, nil
}
