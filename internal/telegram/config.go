package telegram

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
)

// TokenFile is read only on the server's host and by local Telegram setup.
const TokenFile = "telegram-token"

// Configured starts nothing unless both chat and token are present. Invalid
// credentials fail closed and are logged without affecting the server.
func Configured(home string, service control.Service, origin string, logf func(string, ...any)) func(context.Context) {
	loop, err := configured(home, service, origin)
	if err != nil && logf != nil {
		logf("telegram: %v", err)
	}
	if loop == nil {
		return func(context.Context) {}
	}
	loop.Logf = logf
	return loop.Run
}

func configured(home string, service control.Service, origin string) (*Loop, error) {
	tg, err := hosts.ReadTelegram(hosts.ConfigDir(home))
	if err != nil || tg.Chat == 0 {
		return nil, err
	}
	token, err := control.ReadCredential(filepath.Join(hosts.ConfigDir(home), TokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(origin)
	if err != nil {
		return nil, errors.New("invalid dashboard host")
	}
	u.Host = net.JoinHostPort(u.Hostname(), "7332")
	return &Loop{Service: service, Bot: Bot{Token: token}, Chat: tg.Chat, Dashboard: u.String()}, nil
}
