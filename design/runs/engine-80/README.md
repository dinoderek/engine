# Run: engine#80, the two-host fixture (ephemeral)

The acceptance evidence for engine#56 (Claude executors on remote hosts).
Fixture feature engine#80 has four small real tasks: three changes and their
acceptance task. #82 requires `linux`, and the others require `macos`. They
ran under `engine run` on the Mac on 2026-10-25. #82 was placed on the
devbox, executed there and merged from there; the other three ran on the Mac.
#82's claim comment (20:54:08) reads `Host: devbox`, `Run:
engine-82-20261025T205331Z` and `Tmux: engine:engine-82`, and its `run.json`
on the devbox has `"host": "devbox"`.

Setup first. The Mac was brought to `main` at f4d079e and plugin 0.7.6.
`engine setup` there passed every item, including `engine start --check`.
`engine setup --host devbox` then installed f4d079e on the devbox
(`changed, was 3346f0b`), found the plugin, config, sign-in and suspend
items already right, and passed `engine start --check --host devbox`.

## Timeline (UTC)

| Task | Host | Started | Claimed | PR open | Merged | Window |
| --- | --- | --- | --- | --- | --- | --- |
| #81 session slots | mac | 20:53:25 | 20:54:03 | 20:58:48 | 21:00:11 (#97) | reaped 21:11:14 |
| #82 setup toolchain | devbox | 20:53:28 | 20:54:08 | 20:58:31 | 20:59:46 (#96) | reaped 21:11:14, through `release` on the devbox |
| #83 claim tmux | mac | 21:12:18 | 21:12:50 | 21:15:03 | 21:16:28 (#98) | closed itself |
| #84 acceptance | mac | 21:17:32 | 21:18:15 | 21:21:53 | 21:22:57 (#99) | closed itself |

The fixture was created and linted at 20:47. `engine run dinoderek/engine#80`
ran from 20:48:14 in its own tmux session `fixture`, outside session
`engine`, so it held no Mac slot (see Gaps). It set `engine:planned` at
20:48:18 and started #81 and #82 on the next pass, at 20:53. From 20:53:44 it
logged `wait dinoderek/engine#83: no free slot on mac (0 free)` on each
check: the Mac's three slots held this session, the engine#56 coordinator's
window and #81. #83 started a minute after #81's window was reaped. At done it
posted the summary, set `engine:done`, closed #80 as completed (21:23:53) and
exited 0, with no window left to reap.

Each executor had a fresh `claude-opus-5-5` reviewer that approved, after
declining only optional findings. The merge lock was taken in turn: #82 held
it from 20:59:34 to 20:59:53, and #81 from 21:00:00. #84, the last open task,
took none.

After the run, the Mac was rebuilt at `main` (1d9a2e0, Go 1.27.2) and
`engine setup --host devbox` reran. It reported `2. engine: changed (1d9a2e0
go1.27.2, was f4d079e go1.27.3)`, and the preflight passed: the fixture's O2,
live. The Mac's `status` then gave 2 free slots with this session and the
engine#56 coordinator's window both open: the fixture's O1, live.

## Objectives of engine#56

| # | Proved by | Not proved |
| --- | --- | --- |
| O1 host ops and envelope | Live on the devbox: `spawn` and `sync-repo` (through #82's start), `status` (every check, and `ssh devbox engine host status --json`: `protocol 1`, `engine`, `host devbox`), `collect` (#82's run directory, `run.json` and file list), `release` (#82's reap), and an unknown op refused with the envelope and exit 1. On the Mac, the same ops ran in-process. Tests: `internal/hostops`, `internal/remote` | A live protocol mismatch: both hosts ran one build. Covered by `internal/remote` tests |
| O2 `start --host` and `--check --host` | `engine run` starts #82 through `start.Run` with `Host: devbox`, the path `engine start --host` takes; setup's step 7 ran `engine start --check --host devbox`, twice. Tests: `internal/start/remote_test.go`, `ssh_test.go` | `engine start --host` typed by hand: `engine run` takes the same `start.Run` path. The refusals over SSH, live: no refusal came up; `TestRefusalsOnAnotherHostChangeNothing` covers them |
| O3 placement | Live: #82 (`linux`) on the devbox; #81, #83 and #84 (`macos`) on the Mac; #83 held with `no free slot on mac (0 free)` while the devbox had 2 free. Tests: `internal/run/place_test.go` | The tie-breaks and the once-only comment for a task no host can take, live: each task fit one host only (`macos` the Mac, `linux` the devbox). `TestATieOnSlotsAndCapabilitiesGoesToTheLowestName` and `TestATaskNoHostCanTakeIsLoggedEachPassAndCommentedOnce` cover them |
| O4 `ps`, `tell`, `attach` | Live on #82: `engine ps` showed it `working` on `devbox`; `engine attach engine-82` showed the devbox screen over `ssh -t` and detached cleanly; `engine tell engine-82 -` commented, then typed the pointer, which the session queued. `engine ps` with the devbox's address made unresolvable warned and kept #82 at its label state, `running`, not `dead`. Tests: `internal/ps/remote_test.go`, `internal/tell/tell_test.go`, `internal/attach` | |
| O5 session check across hosts | Live: the 60-second check read both hosts' `status`; it saw #81 and #82 waiting after their reports and reaped both at 21:11:14, #82 through the devbox's `release`, which also removed the tmux server's last window there. The `started-on-devbox` note is in the coordinator's directory on the Mac. Tests: `internal/run/remote_test.go` (unreachable host, stall with screen, dead remote session, restart through `release`) | A live stall, dead session or unreachable host during `engine run`: none happened |
| O6 `engine setup [--host]` | Live, as above: `engine setup` on the Mac, and `engine setup --host devbox` twice (`changed` from 3346f0b, then `changed` for the Go). Tests: `internal/setup` | An item left open, live: none was |
| O7 reap at done, no empty note dirs, skill | Each of `engine-80` to `engine-84-coordinator` holds a note; the empty ones in the runs directory are the engine#56 coordinator's, on build 2a08a0c. The skill says both: `execute.md` step 12 (a hands-off run's last action is `tmux kill-window`, since nobody answers after its report) and "Merge lock" (a task with no other open task in its feature takes no lock). Live, #84 took no merge lock, and #83 and #84 closed their own windows. Tests: `internal/run/done_test.go` | Reaping at done, live: no window was left by then. #81 and #82 did not close their windows (Gaps) |
| O8 docs bar | `AGENTS.md`, "Docs", "The bar" (engine#67); `./dev test docs-check` | The bar itself is judgement, not a check |

## Gaps

- **A sibling merged over a moved `main`.** #81 took the lock 14 seconds after
  #96 merged, fetched, saw `main` one commit ahead, and merged anyway, with no
  merge of `main` and no new gate run (`execute.md` step 10 and "Merge lock").
  GitHub does not require an up-to-date branch, and the two PRs touched
  different files, so `main` stayed green (CI at 4b0e14e).
- **Two Sonnet executors did not close their windows.** #81 and #82 ended their
  turn on the report, saying `kill-window` was still to do. #83 and #84, also
  Sonnet, ran it. `engine run` reaped both after ten minutes of waiting. #81's
  window held a Mac slot meanwhile, which delayed #83 by about 11 minutes.
- **Slots held by windows that are not sessions** (engine#68). The engine#56
  coordinator's own window held a Mac slot, so this fixture's coordinator ran
  outside session `engine`. #81 (#97) fixed it on `main`; the running
  coordinators were older builds.
- **One commit, three Go versions.** `go install module@commit` ignores go.mod's
  `toolchain` line, so the Mac built f4d079e with Go 1.27.1 and the devbox with
  1.27.3. #82 (#96) pins the devbox's build. The Mac's own build still follows
  whatever Go it is installed with unless it is built from a checkout, and
  `engine setup` with no `--host` does not check it.
- **The claim's tmux window.** `tmux display-message -p` named the window the
  attached client showed (`engine:run-engine-56`), not this session's
  (`engine:engine-66`). Kickoffs that name `tmux=` hid it. #83 (#98) fixed the
  skill.
- **Small:** claims spell the harness `Claude Code` or `claude-code`, so
  `engine ps` shows `Claude` and `claude-code` in one column; `engine start
  --check` says "this takes a few minutes" and took 15 to 25 seconds.
- **Next:** Codex on both machines (engine#73).
