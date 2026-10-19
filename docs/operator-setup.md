# Operator setup

> **Owns:** the one-time setup an operator does by hand: engine's GitHub identity, its tokens, notifications, and harness sign-in. **Not here:** why engine has its own identity (`coordinator.md`), host config (`hosts-and-sessions.md`).

## Machine user

1. **Create the account.** A new GitHub account, e.g. `<you>-engine`, with its
   own email address (a plus-address of yours works) and 2FA on. GitHub allows
   one free machine account per person.
2. **Grant repo access.** Invite it to each org that owns a target repo. Give
   it **Write** on those repos only, never Admin, so it cannot change
   settings.
3. **Create tokens.** On the machine account, create one fine-grained token
   per org; a fine-grained token has a single owner.
   - **Repositories:** the target repos only.
   - **Read and write:** Contents, Issues, Pull requests. Workflows only if
     tasks edit CI files.
   - **Read only:** Actions, Checks, Commit statuses, Metadata.
   - **Expiry:** set one, and rotate.
   - **Org policy:** the org must allow fine-grained tokens (org Settings →
     Personal access tokens). If it requires approval, approve the request.
4. **Store the tokens** on each host at `~/.config/engine/tokens/<org>`, mode
   `600`. Engine passes the matching token as `GH_TOKEN` to its own `git` and
   `gh` calls and to every session.
5. **Push as the machine user, only from engine's clones.** Engine clones over
   HTTPS, and sets `credential.helper` to `!gh auth git-credential` in each
   clone's local config. Your own checkouts and `gh` login are untouched.
6. **Attribute commits truthfully.** Each engine clone sets the local
   `user.name` and `user.email` to the machine user's noreply address, and the
   harness adds its `Co-Authored-By` trailer. PRs open as the machine user,
   which also means you can approve them.

## Notifications

1. Install GitHub Mobile, signed in as you, with push on for "Participating"
   and "@mentions".
2. Watch each target repo, using Custom → Issues and Pull requests.
3. Engine assigns you to every feature issue it drives, so you count as
   participating. Hard stops @mention you.

## Harnesses (each host)

- **Claude Code:** signed in, with Remote Control on.
- **Codex:** signed in. Its config has auto-review and the sandbox exceptions
  from `hosts-and-sessions.md`, "Permission mode".
- **Both:** each harness has engine's skills installed (`skills/`, with the
  Codex symlink installer).
