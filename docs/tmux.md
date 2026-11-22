# tmux

> **Owns:** how engine drives tmux on a host: its session and windows, typing into them, the server's working directory and scope, and window creation. **Not here:** what a pass reads from a window (`session-signals.md`), how a session starts (`hosts-and-sessions.md`).

Engine owns the tmux session `engine` and sets options only on it and its
windows, never globally or in `~/.tmux.conf`. It targets windows by id
(`@3`): tmux reads `.` as a separator. Windows stay after their command
exits (`remain-on-exit`). Engine types with `send-keys -l`, then `Enter` after
a short delay: input boxes can swallow a newline sent with the text. A usage
limit resume first reads the pane's input line; text already there, such as
the harness's own unsent continue, is sent with `Enter` alone.

**Working directory.** Every tmux command runs from `$HOME`, so no server
engine starts lives in a worktree later removed, failing every later
window. Host status reports one as `tmux_cwd_gone`, with its restart
command; placement skips the host. Engine never kills a server with sessions.

**Creation.** A per-server lock, `tmux-<socket>.lock` in
`~/.local/share/engine`, serialises window creation.

**Scope.** On a systemd host, a session starts in
`systemd-run --user --scope`, so a server it starts outlives the starter's
unit, such as the engine server's. If that fails, tmux starts plainly and the
spawn warns; host status reports a server inside a service as `tmux_unit`.

tmux survives a closed terminal, not a sleeping host: macOS holds off sleep
per window on power; a Linux host must not suspend.
