# Host readiness

> **Owns:** what makes a host ready for a repo's sessions: the `path` prefix, the readiness probe and its cache, and how placement and the operator's notes use it. **Not here:** placement's other rules (`hosts-and-sessions.md`), the coordinator's pass (`coordinator.md`), running setup (`operator-setup.md`).

## The PATH prefix

A host's `path` in `hosts.toml` lists directories, absolute or under `~/`,
put first on `PATH`, in order, for its sessions, the preflight check and the
probe below, such as `path = ["~/.local/share/mise/shims"]`. It is for tools
installed through version manager shims (mise, asdf, nvm), which a tmux
server started over ssh does not see.

## The probe

The `ready` op syncs engine's clone, checks the default branch's head out in
a scratch detached worktree under `~/.local/share/engine/readiness/`, and
runs, in a window of the session `engine` with the host's `path`, as a
session would:

1. `gh auth status -h github.com`;
2. the repo's `./dev doctor`, if its `dev` usage lists `doctor`
   (`project-context.md`).

The window is bounded (`internal/hostops/readyprobe.go`). The worktree is
removed afterwards; one a dead probe left is replaced by the next. No repo
test runs.

**Cache.** The result stays in the repo's readiness directory, keyed by host,
repo, head and a digest of `hosts.toml`. It answers until the key changes or
6 hours pass; `--fresh` probes again.

## Placement and notes

The coordinator asks each host about each coordinated plan's repo in the
background, every few minutes (`internal/controlrun/readiness.go`); a pass
never waits for a probe. Until a host answers, it takes no task of that repo,
and nobody is told. A failed check, or a probe that cannot run, keeps the host
off that repo only. An unreachable host is left to its status.

Each distinct failure (host, repo, check and output) posts one note on the
repo, naming the host, the check, the head and the output; a repeat posts
nothing. A tmux server whose working directory is gone (`hosts-and-sessions.md`)
gets one note per server, naming its restart command.

**After a fix,** `engine setup --host <name>` probes every repo engine has
cloned there afresh, replacing the cache, so the coordinator's next ask
places work there again. Setup also suggests a `path` for shims the host's
`path` leaves out.
