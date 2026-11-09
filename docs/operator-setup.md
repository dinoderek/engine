# Operator setup

> **Owns:** what an operator does by hand: the one-time setup (GitHub access, notifications, harnesses, remote access) and watching a run. **Not here:** how the coordinator reaches hosts (`coordinator.md`), host config (`hosts-and-sessions.md`).

## GitHub access

Each host needs a working `gh` installation with access to engine and its
target repositories, plus normal Git authentication and commit identity.
Engine uses that configuration for GitHub calls, clones, pushes and PRs.
Check access with `gh api user` and `gh repo view <owner/repo>`.

On a Mac, store gh's token in gh's own config file, not the login keychain.
Every window of the `engine` tmux server inherits the context of whoever
started it. After a reboot that is usually the first dispatch, over ssh, and
such a server cannot read the keychain, so every gh call in its sessions fails with a
401. A server started from a Terminal can, so `engine start --check` passes
there until the next reboot. In a Terminal on the Mac, after
`gh auth login -h github.com`, run

```
gh auth token | gh auth login -h github.com --with-token --insecure-storage
```

which keeps the token in plaintext in `~/.config/gh/hosts.yml`. Setup's
`gh auth` step runs `gh auth status` on the host, over ssh for `--host`, and is
open while gh fails there or, on macOS, keeps its token in the keychain.

## Notifications

1. **Hard stops and notices** reach the operator inbox: read it with
   `engine inbox --operator` or in the dashboard (`control-server.md`).
2. **Pull requests:** to hear about PRs and CI, run GitHub Mobile signed in as
   you, and watch each target repo, using Custom → Pull requests.

## Each host

**The Mac, by hand.** Install `go`, `tmux`, `gh`, `git` and `claude` (and
`sqlite3` on a Codex host, to read the daemon's connection), and put
`~/.local/bin` on `PATH`. Write `~/.config/engine/host` (the host's logical
name, one line, as attempts and every surface show it: `hosts-and-sessions.md`, "Hosts") and
`~/.config/engine/hosts.toml` (the same file on every host). Install engine
with `GOBIN=~/.local/bin go install ./cmd/engine` from a checkout, and the
plugin for yourself with `claude plugin marketplace add dinoderek/engine` and
`claude plugin install engine@engine --scope user`, and for Codex
`codex plugin marketplace add dinoderek/engine` and `codex plugin add
engine@engine`. Sign in to Claude Code
with your claude.ai account (`/login`): Remote Control rejects API keys and
`setup-token`. Sign in to Codex (`codex login`). Setup copies the Mac's
engine commit and Claude plugin version to the other hosts, so update both here
first. Setup installs the Codex plugin only into engine's own Codex home, and
embedded sessions that cannot join engine's daemon use the host's own: install
it there by hand on each Codex host,
with `codex plugin marketplace add dinoderek/engine` and `codex plugin add
engine@engine`. Setup prints these when the plugin is missing there. In a
Codex session engine did not launch, Codex runs the plugin's hooks only once
reviewed: review them in the first such session after each install and
update (`codex plugin update`).

**Every other host: `engine setup --host <name>`**, run from the Mac and safe
to rerun. It brings the host to the Mac's pushed engine commit, plugin and
config, and ends with `engine start --check --host <name>`, which reads the
host's protocol from the refusal of the undefined op `protocol`, then streams
`ssh <address> engine start --check` and keeps its exit code. The engine is built
with `GOTOOLCHAIN` set to the `toolchain` line of go.mod at that commit, read
from GitHub, and counts as current only on that commit and that Go. It changes
only engine's own files (`~/.config/engine/`, `~/.local/bin/engine`,
`~/.local/share/engine/`), Claude's plugin, and Codex's plugin and daemon in
engine's own Codex home; Go's own cache holds the toolchain download. For anything else (`~/.bashrc`, `sudo`, `/login`) it prints one exact
command, and it exits 1 while any item is open. It refuses a build that is
dirty or not on GitHub, and a name that is not a machine in `hosts.toml`.
With no `--host` it checks this host, apart from the Codex steps, which act.
A host gets only the steps of the harnesses it lists: the host's own steps
first, then each harness's, then gh's sign-in and the preflight, which checks
every harness the host lists, the default first.

**Codex, on a host that lists it.** The Mac too. Setup checks that the pinned Codex runs and is signed in, then builds engine's own
Codex home, and never edits `~/.codex`. Starting engine's
daemon turns on remote control, which opens a connection to OpenAI's service for
the ChatGPT and Codex apps. Signing in is yours: setup prints `codex login`, or,
on another host, `ssh -t <address> codex login --device-auth`. After a reboot, rerun setup
to start engine's daemon. Setup records the daemon it starts; a running
daemon it did not record leaves setup open, since it may hold a session's
environment: finish its sessions, run the stop command setup prints, and
rerun setup. Setup reports the operator's own remote-control errors and
ignored config keys, and fixes none.

**Claude's one-time prompts.** Nobody is there to answer them in an engine
window. When the preflight finds one, run `claude --permission-mode auto
--remote-control` once by hand on that host, in a folder you trust, and
answer every prompt, such as enabling Remote Control or the auto mode notice.
Run `engine setup` again after updating Claude Code, the plugin or `engine`.

**The plugin in a target repo.** A repo enables the `engine` plugin in its
`.claude/settings.json`, as this repo does. Update the user's copy with
`claude plugin update engine@engine --scope user`: without `--scope`, in a
checkout that enables the plugin, it updates the project's copy instead.

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
4. **Install the tools** `go`, `tmux`, `gh` and `git` on the devbox,
   `claude` if it runs Claude, and `sqlite3` if it runs Codex.
   `engine setup` lists what is missing and prints the `PATH` line for
   `~/.bashrc`, and the `sudo systemctl mask` line that stops the devbox
   suspending. Sign in once over SSH: `ssh -t <address> claude /login`.

## Each target repo

- Confirm the host's `gh` and Git access to the repo.
- Enable the `engine` plugin in its `.claude/settings.json`.

## Watching a run

Once you approve a plan and run `engine run <plan>`, the coordinator inside
`engine serve` runs it to `done`; nobody else watches it. Check in when you
like:

- `engine ps`: each task's state and attempt; `engine attempt list <task>`
  gives its host and window, and `engine attach` opens it.
- `engine inbox --operator`: hard stops, notices and rulings digests. Answer a
  question with `engine reply <sequence> <text>`, settle it with
  `engine resolve` (which also restores the task's state), or answer a
  waiting task with `engine tell <task> <text>` (once on the plan wakes each
  waiting task in it).
- `engine updates <plan>` and `engine updates <task>`, from your read cursor
  (`--after <sequence>`).
- `engine ui`: the same reads in a browser (`control-server.md`,
  "Dashboard").

A task with many `review` rulings had a weak card (`execution-protocol.md`,
"Rulings and hard stops"): raise it at the next Prepare.

**Engine bugs.** When the coordinator misses something (a stall it never
raised, a merge it never made, a state that is wrong), file an issue on
`dinoderek/engine` with the evidence: the task's `engine updates`, the state
`engine ps` showed and when, and what you did instead. Work around it only
through `engine` commands; never restart `engine serve` to clear it.
