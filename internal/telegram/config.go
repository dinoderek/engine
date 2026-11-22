package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
)

// TokenFile is read only on the server's host and by local Telegram setup.
const TokenFile = hosts.TelegramTokenFile

// Configured starts nothing unless both chat and token are present. Invalid
// credentials fail closed and are logged without affecting the server.
func Configured(home string, service control.Service, logf func(string, ...any)) func(context.Context) {
	loop, err := configured(home, service)
	if err != nil && logf != nil {
		logf("telegram: %v", err)
	}
	if loop == nil {
		return func(context.Context) {}
	}
	if loop.Dashboard == "" && logf != nil {
		logf("telegram: dashboard links omitted; set [telegram] dashboard_url in config.toml to the published engine ui HTTP(S) origin")
	}
	loop.Logf = logf
	return loop.Run
}

func configured(home string, service control.Service) (*Loop, error) {
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
	return &Loop{Service: service, Bot: Bot{Token: token}, Chat: tg.Chat, Dashboard: tg.DashboardURL}, nil
}
