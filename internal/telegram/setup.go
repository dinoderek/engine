package telegram

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
)

// Setup pairs the local bot with one private chat, after showing its sender.
// The bot must have exactly one pending message, sent by the operator.
func Setup(ctx context.Context, home string, in io.Reader, out io.Writer, bot Bot) error {
	dir := hosts.ConfigDir(home)
	cfg, err := hosts.ReadTelegram(dir)
	if err != nil {
		return err
	}
	if cfg.Chat != 0 {
		return errors.New("telegram chat is already configured; setup does not replace it")
	}
	bot.Token, err = control.ReadCredential(filepath.Join(dir, TokenFile))
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Send one message to your bot in a private chat, then press Enter. Keep engine serve stopped while pairing.")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return errors.New("setup cancelled before reading Telegram")
	}
	updates, err := bot.Updates(ctx, 0, 0)
	if err != nil {
		return err
	}
	var message *Message
	for _, u := range updates {
		if u.Message == nil {
			continue
		}
		if message != nil {
			return errors.New("expected one pending message; clear the bot's pending updates and send one message")
		}
		message = u.Message
	}
	if message == nil {
		return errors.New("no message received; send one message to the bot and retry")
	}
	if message.Chat.Type != "private" {
		return errors.New("setup requires a private chat with the bot")
	}
	fmt.Fprintf(out, "Sender: %s %s (@%s), user %d; chat %d\nUse this chat? [y/N] ", message.From.Name, message.From.Last, message.From.Username, message.From.ID, message.Chat.ID)
	if !scanner.Scan() || !strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
		fmt.Fprintln(out, "Telegram setup cancelled; config unchanged.")
		return nil
	}
	if err = hosts.WriteTelegramChat(dir, message.Chat.ID); err != nil {
		return err
	}
	fmt.Fprintln(out, "Telegram chat configured. Start engine serve on this host to receive alerts.")
	if cfg.DashboardURL == "" {
		fmt.Fprintln(out, "For dashboard links, set [telegram] dashboard_url in config.toml to the published engine ui HTTP(S) origin. Without it, alerts and answer buttons work with no link.")
	}
	return nil
}
