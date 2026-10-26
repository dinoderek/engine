# Operator setup

> **Owns:** the one-time setup an operator does by hand: engine's GitHub identity, its token, notifications, harnesses and remote access. **Not here:** why engine has its own identity (`coordinator.md`), host config (`hosts-and-sessions.md`).

## Machine user

1. **Account.** A separate GitHub account (here `dinoderek-agents`), with a
   verified email and 2FA. GitHub allows one free machine account per person.
2. **Access.** Add it to each target repo as an outside collaborator with
   **Write**. Never give it Admin and never make it an org member. Its
   collaborator list is the real boundary on what engine can touch.
3. **Token.** Use a **classic** token on the machine account, with scopes
   `repo`, `workflow` and `read:org`, and an expiry you rotate.
   - Fine-grained tokens don't work here. They cannot reach repos where the
     account is an outside collaborator, or repos owned by another personal
     account.
   - A classic token reaches only what the account can, so the token is
     limited by step 2.
   - Each org must allow classic tokens: org Settings → Personal access
     tokens.
4. **Store it** on each host at `~/.config/engine/github-token`, with mode
   `600`, and never in shell history.
5. **Pass it, don't log in with it.** Engine passes the token as `GH_TOKEN`
   to its own `git` and `gh` calls and to every session. Your own `gh` login
   stays yours.
6. **Push from engine's clones only.** Engine clones over HTTPS and refuses
   to clone or sync when a `url.<base>.insteadOf` or `pushInsteadOf` rule in
   your git config would send an engine clone over SSH, so limit such a rule
   to your own checkouts (an `includeIf "gitdir:"`).
7. **Who opens PRs.** The machine user, which also means you can
   approve them.

## Notifications

1. **Phone:** GitHub Mobile, signed in as you, with push on for
   "Participating" and "@mentions".
2. **Repos:** watch each target repo, using Custom → Issues and Pull requests.
3. **Engine's side:** engine assigns you to every feature issue it drives, so
   you count as participating. Hard stops @mention you.

## Each host

**The Mac, by hand.** Install `go`, `tmux`, `gh`, `git` and `claude`, and put
`~/.local/bin` on `PATH`. Write `~/.config/engine/host` (the host's logical
name, one line, as claims post it: `hosts-and-sessions.md`, "Hosts") and
`~/.config/engine/hosts.toml` (the same file on every host). Install engine
with `GOBIN=~/.local/bin go install ./cmd/engine` from a checkout, and the
plugin for yourself with `claude plugin marketplace add dinoderek/engine` and
`claude plugin install engine@engine --scope user`, and for Codex
`codex plugin marketplace add dinoderek/engine` and `codex plugin add
engine@engine`. Sign in to Claude Code
with your claude.ai account (`/login`): Remote Control rejects API keys and
`setup-token`. Sign in to Codex (`codex login`). Setup copies the Mac's
engine commit and Claude plugin version to the other hosts, so update both here
first.

**Every other host: `engine setup --host <name>`**, run from the Mac and safe
to rerun. It brings the host to the Mac's pushed engine commit, plugin and
config, and ends with `engine start --check --host <name>`. The engine is built
with `GOTOOLCHAIN` set to the `toolchain` line of go.mod at that commit, read
from GitHub, and counts as current only on that commit and that Go. It changes
only engine's own files (`~/.config/engine/`, `~/.local/bin/engine`,
`~/.local/share/engine/`), Claude's plugin, and Codex's plugin and daemon in
engine's own Codex home; Go's own cache holds the toolchain download. For anything else (`~/.bashrc`, `sudo`, `/login`) it prints one exact
command, and it exits 1 while any item is open. It refuses a build that is
dirty or not on GitHub, and a name that is not a machine in `hosts.toml`.
With no `--host` it only checks this host.

**Codex, on a host that lists it.** The Mac too: setup acts on Codex there.
It checks that the pinned Codex runs and is signed in, then builds engine's own
Codex home (`codex.md`, "Setup"), and never edits `~/.codex`. Starting engine's
daemon turns on remote control, which opens a connection to OpenAI's service for
the ChatGPT and Codex apps. Signing in is yours: setup prints `codex login`, or,
on another host, `ssh -t <address> codex login --device-auth`. The daemon keeps
the token it started with: after rotating it, stop the daemon with
`CODEX_HOME=~/.local/share/engine/codex codex app-server daemon stop` and
rerun setup. Setup reports the operator's own remote-control errors and ignored
config keys, and fixes none.

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
4. **Install the tools** `go`, `tmux`, `gh`, `git` and `claude` on the devbox.
   `engine setup` lists what is missing and prints the `PATH` line for
   `~/.bashrc`, and the `sudo systemctl mask` line that stops the devbox
   suspending. Sign in once over SSH: `ssh -t <address> claude /login`.

## Each target repo

- Add the machine user (above).
- Run `engine labels sync <owner/repo>` once, and again whenever the label set
  in `internal/tracker` changes. It never deletes a label.
- Enable the `engine` plugin in its `.claude/settings.json`.
