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
  call; without it the guard fails open.
- **Claude Code:** signed in, with Remote Control working from the Claude app.
- **Codex:** signed in. Engine passes auto-review and the sandbox exceptions
  per session (`hosts-and-sessions.md`, "Permission mode").
- **Host name:** its logical name (`mac`, `devbox`) on one line in
  `~/.config/engine/host`. Claims post this name, never the hostname
  (`hosts-and-sessions.md`, "Hosts").
- **Skills (Claude Code):** the `engine` plugin from this repo's marketplace,
  `.claude-plugin/marketplace.json`. A target repo enables it in its
  `.claude/settings.json`, as this repo does; or install it for yourself with
  `/plugin install engine --marketplace dinoderek/engine`. Codex has no
  install yet.
- **Sessions started by hand** use the token file for their `gh` calls, so
  their @mentions reach you. Their `git push` uses the checkout's own
  credentials.
- **SSH (the fallback route to any session):**
  - every host on the same Tailscale tailnet as the phone;
  - key-based SSH between the coordinator host and every other host;
  - on macOS, Remote Login turned on.

## Each target repo

- Add the machine user (above).
- Run `engine labels sync <owner/repo>` once, and again whenever the label set
  in `internal/tracker` changes. It never deletes a label.
- Enable the `engine` plugin in its `.claude/settings.json`.
