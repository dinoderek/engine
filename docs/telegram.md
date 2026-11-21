> **Owns:** Telegram operator alerts, one-tap answers and bot pairing. **Not here:** the operator inbox and question records (`control-server.md`), dashboard access (`dashboard.md`), host setup (`operator-setup.md`).

# Telegram

`engine serve` runs one Telegram integration beside the coordinator, even with no
UI running. It uses outbound HTTPS only. Keep one server polling a bot; neither
`engine ui` nor a worker polls it. Without both a configured chat and a token file,
it starts no Telegram work. Invalid configuration or credentials log a failure
and leave the server running.

## Pair on the server's host

1. Create a bot with Telegram's BotFather. Put its token alone in
   `~/.config/engine/telegram-token`, a regular file readable only by its owner
   (`chmod 600`). The token follows the operator credential's validation rules.
2. Keep `engine serve` stopped while pairing. Run `engine setup telegram` on
   that host, send exactly one message to the bot in your private chat, then
   press Enter in the command.
3. Check the displayed sender, username, user ID and chat ID. Enter `y` to
   confirm; any other answer or end of input leaves configuration unchanged.
   Confirmation adds `[telegram] chat = <id>` to
   `~/.config/engine/config.toml`, preserving its other settings. Pairing refuses
   to replace an existing chat. With no message, multiple pending messages or a
   group chat, setup writes nothing and explains the problem.
4. For dashboard links, optionally add `dashboard_url = "https://dashboard.example"`
   under `[telegram]` in that file. Use the published dashboard origin, including
   its scheme and any port, as the phone reaches it. For the tailnet proxy and
   `--host`, load `dashboard-writes.md`. With no URL, alerts and choice answers
   still work, links are omitted, and the server logs how to configure them.
5. Start `engine serve`. To disable Telegram, remove the chat setting or token
   file and restart the server. To pair a replacement chat, remove the chat
   setting first and repeat setup.

The token stays on the server's host. Question text and informational operator
messages go to Telegram, so choose a bot and private chat you control.

## Alerts and answers

Each open question gets its own audible message: item ID, title, hard-stop class,
plain question text truncated to 1000 Unicode characters, and the configured
dashboard link when present.
Each choice gets a button; a star marks the recommendation. Questions without
choices can be answered through the CLI or session. Links use `dashboard_url`
exactly; it accepts an HTTP(S) origin without credentials, a path other than
`/`, a query or a fragment. The API address never determines the dashboard link.

A tap is accepted only from the configured chat and the question's recorded bot
message. It resolves with the choice's exact text. A callback's request key is
stable, so redelivery records the answer once. The message loses its buttons and
shows the answer; a question settled elsewhere shows “Already answered” when
tapped. A question answered through another surface is also edited to show its
answer. Informational messages are batched into one silent digest at most once
per minute; acknowledged entries are marked in that digest.

Delivery scans run every five seconds. Callback polling waits up to twenty
seconds, independently of scans and coordinator passes. Failed requests retry
with exponential backoff capped at one minute; Telegram's longer retry delay is
respected. Errors are logged without the token. Telegram failures never hold a
coordinator pass or an API write across a network request.

## Delivery state

The server database migrates on open to include a delivery table and polling
state. Pending entries are recorded before sending; successful sends record the
bot's message ID. The highest recorded update is the delivery cursor. Pending
entries remain eligible regardless of that cursor; an entry settled before its
send succeeds is retired. Poll offsets and the digest time persist too. Backups
include these tables; when changing storage or backups, load `control-server.md`.

A normal restart neither resends recorded deliveries nor skips pending entries.
Delivery is at least once: if Telegram accepted a send but its response or the
local commit is lost, retrying can produce a duplicate alert. The Bot API has no
send idempotency key. This narrow failure window affects notifications;
question resolution remains exactly once through server request keys. Message
edits retry independently, so an uneditable old message cannot block later alerts
or answers. Presentation intent persists before callback polling advances.
