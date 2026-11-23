# Operator setup

> **Owns:** what an operator does by hand: the one-time setup (GitHub access, notifications, harnesses, remote access), upgrading the fleet and watching a run. **Not here:** how the coordinator reaches hosts (`coordinator.md`), host config (`hosts-and-sessions.md`).

## GitHub access

Each host needs `gh` access to engine and its target repositories, Git
authentication and commit identity, which engine uses for GitHub calls,
clones, pushes and PRs. Check with `gh api user` and `gh repo view <owner/repo>`.

On a Mac, keep gh's token out of the login keychain: the `engine` tmux server
starts over ssh after a reboot and cannot read it, so every gh call in its
sessions fails with a 401 (`engine start --check` passes from a Terminal until
the next reboot). In a Terminal, after `gh auth login -h github.com`, run

```
gh auth token | gh auth login -h github.com --with-token --insecure-storage
```

which keeps the token in plaintext in `~/.config/gh/hosts.yml`.

## Notifications

For Telegram alerts and pairing, load `telegram.md`.

1. **Hard stops and notices** reach the operator inbox: read it with
   `engine inbox --operator` or in the dashboard (`dashboard.md`).
2. **Pull requests:** to hear about PRs and CI, run GitHub Mobile signed in as
   you, and watch each target repo, using Custom → Pull requests.

## Each host

**The Mac, by hand.** Install `go`, `tmux`, `gh`, `git` and `claude` (and
`sqlite3` only for Codex hosts), and put
`~/.local/bin` on `PATH`. Write `~/.config/engine/host` (the host's logical
name: `hosts-and-sessions.md`, "Hosts") and `~/.config/engine/hosts.toml` (the same on every host). Install engine
with `GOBIN=~/.local/bin go install ./cmd/engine` from a checkout, and the
Claude plugin for yourself with `claude plugin marketplace add dinoderek/engine` and
`claude plugin install engine@engine --scope user`. Sign in to Claude Code
with your claude.ai account (`/login`): Remote Control rejects API keys and
`setup-token`. Sign in to Codex (`codex login`). Setup copies the Mac's
engine commit and Claude plugin version to the other hosts, so update both here
first. Setup installs and updates the Codex plugin in both Codex homes, engine's own
and yours (embedded sessions that cannot join engine's daemon use yours), through `codex plugin`. Codex runs the
plugin's hooks in a session engine did not launch only once reviewed: review
them in the first such session after each install or update.

**Every other host: `engine setup --host <name>`**, run from the Mac and safe
to rerun. It brings the host to the Mac's pushed engine commit, plugin and
config, and ends with `engine start --check --host <name>`, a suggested
`path` for any mise, asdf or nvm shims the host's `path` leaves out, and a
fresh readiness probe of every repo engine has cloned there
(`readiness.md`). It builds engine with that commit's go.mod
`toolchain`, read from GitHub. Setup changes only engine's own files
(`~/.config/engine/`, `~/.local/bin/engine`, `~/.local/share/engine/`, and on the Mac the watcher's agent in `~/Library/LaunchAgents/`), Claude's plugin, Codex's plugin in your Codex home (through `codex plugin` only,
never another file in `~/.codex`), and Codex's plugin and daemon in engine's own
Codex home. For anything else (`~/.bashrc`, `sudo`, `/login`) it prints one
exact command, and it exits 1 while any item is open. With
no `--host` it checks this host. A host gets only the steps of the harnesses it
lists.

**Codex, on a host that lists it.** The Mac too. Setup checks that the pinned Codex runs and is signed in, then builds engine's own
Codex home. Starting engine's
daemon turns on remote control, which opens a connection to OpenAI's service for
the ChatGPT and Codex apps. Signing in is yours: setup prints `codex login`, or,
on another host, `ssh -t <address> codex login --device-auth`. After a reboot rerun
setup to start engine's daemon. Setup records the daemon it starts; one it did
not start leaves setup open, since it may hold a session's
environment: finish its sessions, run the stop command setup prints, and rerun
setup.

**Claude's one-time prompts.** Nobody is there to answer them in an engine
window. When the preflight finds one, run `claude --permission-mode auto
--remote-control` once by hand on that host, in a folder you trust, and
answer every prompt.
Rerun `engine setup` after updating Claude Code, the plugin or `engine`.

**The plugin in a target repo.** A repo enables the `engine` plugin in its
`.claude/settings.json`, as this repo does. Update the user's copy with
`claude plugin update engine@engine --scope user`; without `--scope`, in a
checkout that enables it, the project's copy updates instead.

**SSH, the route to any session:**

- every host on the same Tailscale tailnet as the phone;
- plain OpenSSH, never Tailscale SSH: its check mode asks for a browser
  approval, which an unattended coordinator cannot give. The keys of the
  coordinator's host and the phone's SSH app go in every other machine's
  `authorized_keys`;
- on macOS, Remote Login turned on.

## A remote devbox

A Linux machine on the tailnet, set up from the Mac. Do these by hand once,
then run `engine setup --host devbox`.

1. **Authorize keys.** On the devbox, append to `~/.ssh/authorized_keys` the
   Mac's `~/.ssh/id_ed25519.pub` and the phone SSH app's key.
2. **Switch to OpenSSH.** First make sure you can reach the devbox's own
   console: if key auth then fails, that is the only way back. On the devbox,
   install `openssh-server` (`sudo apt install openssh-server`), then run this;
   Tailscale SSH goes off only once sshd is listening:

   ```bash
   sudo systemctl enable --now ssh && ss -ltn | grep -q ":22 " && { ! sudo ufw status | grep -q "Status: active" || sudo ufw allow in on tailscale0 to any port 22 proto tcp; } && sudo tailscale set --ssh=false
   ```

   To undo it from that console: `sudo tailscale set --ssh`.
3. **Check from the Mac.** On first contact, accept the devbox's host key
   (`-o StrictHostKeyChecking=accept-new`); if it changed when sshd took over
   from Tailscale, check the fingerprint on the devbox, then
   `ssh-keygen -R <tailscale name>`. Then
   `ssh -o BatchMode=yes <user>@<tailscale name> true` must succeed with no
   prompt. That string is the devbox's `address` (`hosts-and-sessions.md`,
   "Names and addresses"), which goes in `hosts.toml` on the Mac.
4. **Install the tools** `go`, `tmux`, `gh`, `git` and `lsof` on the devbox, plus
   `claude` if it runs Claude and `sqlite3` if it runs Codex. `engine setup`
   does not check `lsof` or `sqlite3`, and prints the `PATH` line and the `sudo systemctl
   mask` line that stops suspending. Sign in once over SSH:
   `ssh -t <address> claude /login`.

## Upgrading

`engine upgrade`, from any host in `hosts.toml`, brings every host to the
head of `main`, mid-plan too. It stops the server's unit and backs up the
database with the old binary, installs engine and your plugins here, sets
up the server's host and starts the server, then sets up every other host.
The server starts again even when a step fails. `--dry-run` only prints
the steps.

Busy tasks carry on; a session that dies meanwhile is an engine bug
(`session-signals.md`, "Upgrade"; old plugin versions: `harnesses.md`).

It refuses, changing nothing, when this host's `config.toml` sets no
`[server] host`, or the server's host records no `services` table, does
not answer, or its unit is not active. An unreachable or open host stops
no others: run the commands the report lists, then rerun it. Restart
`engine ui` yourself. A Mac server host has no unit: upgrade it by hand,
in the order above (to back up, `control-server.md`, "Start"),
with `engine setup` and `engine setup --host <name>` for the setups.

**The server's unit.** Write it once, by hand, on the server's host, as
`~/.config/systemd/user/engine-server.service`. It runs `~/.local/bin/engine`,
the binary setup replaces; keep no copy elsewhere.

```ini
[Service]
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
ExecStart=%h/.local/bin/engine serve --foreground --listen <tailscale IP>:7331 --advertise http://<tailscale name>:7331
Restart=on-failure

[Install]
WantedBy=default.target
```

A user unit reads no shell rc file, so its `PATH` must find `tmux`, `ssh`,
`gh`, `git` and the harness CLIs, and ssh to other hosts must work without an
interactive agent (a usable key, or `SSH_AUTH_SOCK` in the unit). Then run `systemctl --user daemon-reload`,
`systemctl --user enable --now engine-server.service` and
`loginctl enable-linger <user>`, so the unit outlives your login, and record
it in the host's `hosts.toml` entry as `[host.<name>.services]` with
`manager = "systemd"` and `server = "engine-server.service"`.

On a Linux server host, setup's `server unit` step only reads the unit,
and stays open for each fault, or a state it cannot read: the unit is not `enabled`
(`systemctl --user enable`); `Restart` is `no` (`systemctl --user edit`, set
`Restart=on-failure`); linger is off (`loginctl enable-linger <user>`); or
host status reports `tmux_unit`, a session tmux inside the unit that a stop
would end (move it out, ending nothing: `recovery.md`, "2. The server is
upgraded or restarted"). A server host that records no `services` gets a
note: its server will not return after a reboot.

## Watching a run

After `engine run <plan>`, the coordinator runs the plan to `done`; check in
when you like:

- `engine ps` shows each task's state and attempt; `engine attach` opens its
  window.
- `engine inbox --operator`: hard stops, notices and rulings digests. Answer a
  question with `engine reply <sequence> <text>`, settle it with
  `engine resolve` (which also restores the task's state), or answer a
  waiting task with `engine tell <task> <text>` (once on the plan wakes each
  waiting task in it).
- `engine updates <plan>` and `engine updates <task>`, from your read cursor
  (`--after <sequence>`).
- `engine task stop <task>` ends a session and holds the task in `todo`,
  keeping its worktree and branch (`control-server.md`).
- `engine ui`: the same reads in a browser (`dashboard.md`).
- **A server that does not answer:** the watcher, which `engine setup`
  installs on the Mac, says so on Telegram (load `telegram.md`). From any
  host, `engine server status` shows its unit, API, database, disk and last 50
  log lines, and `engine server restart` restarts its unit; both take
  `--json`. The database check is unknown without `sqlite3` on the server.

A task with many `review` rulings had a weak card (`execution-protocol.md`,
"Rulings and hard stops"): raise it at the next Prepare.

**Engine bugs.** When the coordinator misses something (a stall it never
raised, a merge it never made, a state that is wrong), file an issue on
`dinoderek/engine` with the evidence: the task's `engine updates`, the state
`engine ps` showed and when, and what you did instead. Work around it only
through `engine` commands; never restart a server that answers to clear it.
