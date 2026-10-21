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
6. **Push from engine's clones only.** Engine clones over HTTPS and sets
   `credential.helper` to `!gh auth git-credential` in each clone's local
   config. Your SSH-key checkouts are untouched.
7. **Attribute commits truthfully.** Each engine clone sets the local
   `user.name` and `user.email` to the machine user's noreply address, and the
   harness adds its `Co-Authored-By` trailer. PRs open as the machine user,
   which also means you can approve them.

## Notifications

1. **Phone:** GitHub Mobile, signed in as you, with push on for
   "Participating" and "@mentions".
2. **Repos:** watch each target repo, using Custom → Issues and Pull requests.
3. **Engine's side:** engine assigns you to every feature issue it drives, so
   you count as participating. Hard stops @mention you.

## Each host

- **Tools:** `tmux`, `gh` and `git`, plus `go` to build engine.
- **engine on `PATH`:** `go install ./cmd/engine` from a checkout, with Go's
  install directory on `PATH`. The plugin's merge guard runs it on every Bash
  call; without it, or with a binary too old for the plugin's hooks, the guard
  fails open. Update `engine` on each host before taking a plugin update.
- **Claude Code:** signed in with your claude.ai account (`/login`), with
  Remote Control working from the Claude app. Remote Control rejects API keys
  and `setup-token`, so on a remote host sign in once over SSH.
- **Codex:** signed in. Engine passes auto-review and the sandbox exceptions
  per session (`hosts-and-sessions.md`, "Permission mode").
- **Host name:** its logical name (`mac`, `devbox`) on one line in
  `~/.config/engine/host`. Claims post this name, never the hostname
  (`hosts-and-sessions.md`, "Hosts").
- **Host config:** the same `~/.config/engine/hosts.toml` as every other
  host, with an entry for this host as `hosts-and-sessions.md`, "Hosts",
  specifies. You write both files by hand.
- **Skills (Claude Code):** the `engine` plugin from this repo's marketplace,
  `.claude-plugin/marketplace.json`. A target repo enables it in its
  `.claude/settings.json`, as this repo does; or install it for yourself with
  `/plugin install engine --marketplace dinoderek/engine` in a session, or
  from a shell with `claude plugin marketplace add dinoderek/engine` and
  `claude plugin install engine@engine`. Codex has no install yet.
- **Sessions started by hand** run their `gh` calls as `engine gh …`, which
  reads the token file, so their @mentions reach you. Their `git push` uses the checkout's own
  credentials.
- **SSH (the fallback route to any session):**
  - every host on the same Tailscale tailnet as the phone;
  - plain OpenSSH, never Tailscale SSH: its check mode asks for a browser
    approval, which an unattended coordinator cannot give. The keys of the
    coordinator's host and the phone's SSH app go in every other machine's
    `authorized_keys`;
  - on macOS, Remote Login turned on.

## A remote devbox

A Linux machine on the tailnet, set up from the coordinator's host (the Mac).
It gets every "Each host" item above; these steps are what differs.

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
   "Names and addresses").
4. **Devbox-only details:**
   - engine needs no checkout: `GOBIN=~/.local/bin go install
     github.com/dinoderek/engine/cmd/engine@main`;
   - the non-interactive `PATH` must include `~/.local/bin` (and any tool
     manager's shims), so export it at the top of `~/.bashrc`, above the
     line that returns for non-interactive shells;
   - copy `hosts.toml` and `github-token` from the Mac over SSH, with mode
     `600`;
   - no suspend: `sudo systemctl mask sleep.target suspend.target
     hibernate.target hybrid-sleep.target`.
5. **Check the coordinator's view.** From the Mac,
   `ssh -o BatchMode=yes <address> 'command -v engine tmux gh claude codex'`
   must list every tool: it runs in the non-interactive shell the coordinator
   uses.

## Each target repo

- Add the machine user (above).
- Run `engine labels sync <owner/repo>` once, and again whenever the label set
  in `internal/tracker` changes. It never deletes a label.
- Enable the `engine` plugin in its `.claude/settings.json`.
