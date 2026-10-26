# Run: engine#116, the two-host Codex fixture (ephemeral)

The acceptance evidence for engine#74 (Codex executors on the Mac and the
devbox). Fixture feature engine#116 has four small real tasks: three changes
and their acceptance task. #118 runs on Codex and requires `macos`. #119 runs
on Codex and requires `linux`. #117 and the acceptance task #120 run on
Claude on the Mac. They ran under `engine run` on the Mac on 2026-10-26.
#119 was placed on the devbox, executed there and merged from there. Its
claim (11:54:57) reads `Host: devbox` and `Harness: Codex 0.164.0
(gpt-6.1-sol)`, and its `run.json` on the devbox has `"host": "devbox"` and
`"harness": "codex"`. #118 ran on the Mac. Its claim (11:54:54) reads `Host:
mac` and `Harness: Codex 0.163.0 (gpt-6)`, and its `run.json` has
`"harness": "codex"`.

Setup first. The Mac was built from `main` at 55c0f72 with go.mod's toolchain.
`engine setup` on the Mac and `engine setup --host devbox` (already on
55c0f72) passed every item except 10 and 12 on both hosts. Item 10 was open
because the Codex plugin is missing from the operator's own `~/.codex`. Setup
leaves that install to the operator, so item 12, the preflight, did not run
(see Gaps). The four preflights were run by hand instead, and all passed.
`engine start --check --harness codex` and `--harness codex --host devbox`
each joined remote control, closed its window, did every step, had its hooks
write the state (last `waiting`), and had no request denied. `engine start
--check` and `--check --host devbox` passed for Claude.

## Timeline (UTC)

| Task | Harness | Host | Started | Claim | PR open | Merged | Window |
| --- | --- | --- | --- | --- | --- | --- | --- |
| #118 ps harness names | codex | mac | 11:54:07 | 11:54:54 | 11:56:58 | 11:58:06 (#121) | closed itself |
| #119 setup's plugin reason | codex | devbox | 11:54:11 | 11:54:57 | 12:01:04 | 12:02:23 (#123) | closed itself |
| #117 check duration | claude | mac | 11:54:15 | 11:54:51 | 11:57:02 | 11:58:24 (#122) | closed itself |
| #120 acceptance | claude | mac | 12:03:39 | 12:04:28 | none | closed 12:05:31, no change needed | closed itself |

The fixture was created and linted at 11:48. `engine run
dinoderek/engine#116` ran from 11:48:55 in window `engine:run-engine-116`,
inside session `engine`, and held no slot. It set `engine:planned` at
11:48:59, started #117 to #119 on the next pass, set `engine:verifying` at
12:03:43, and posted the summary at 12:06:01. Then it set `engine:done`,
closed #116 as completed and exited, with no window left to reap. The
engine#74 coordinator (`run-engine-74`) ran alongside it throughout.

Each executor got one fresh reviewer, and each reviewer approved in one round.
The Codex reviewers were Codex subagents on `gpt-6.1-sol`. #117's was a
Claude subagent on the `opus` alias. The merge lock went to #118 from 11:57:01
to 11:58:22 and to #119 from 12:01:19 to 12:02:30. #119 merged `main` in
before merging (338e382). #117 took no lock (see Gaps). #120, the last open
task, needed none, and passed on `main` at 8b64800 without a PR.

## Codex sessions' permission logs

Every request in both logs was `left to auto-review`, and none was denied.
Neither session sat on a prompt.

`engine-118-20261026T115410Z/permissions.jsonl` (Mac), 6 requests:

| Time | Command |
| --- | --- |
| 11:54:28 | `git fetch origin && git merge origin/main` |
| 11:55:22, 11:55:55 | `./dev test` |
| 11:56:43 | `git add … && git commit …` |
| 11:57:11 | `git fetch origin && git merge origin/main` |
| 11:58:28 | `./dev worktree release task/engine-118` |

`engine-119-20261026T115414Z/permissions.jsonl` (devbox), 10 requests:

| Time | Command |
| --- | --- |
| 11:54:39, 11:55:45, 11:58:52, 12:01:20 | `git fetch origin` |
| 11:54:58 | `git merge origin/main` |
| 11:55:10 | `./dev test` |
| 11:55:35 | `git add … && git commit …` |
| 11:58:59 | `git merge --no-edit origin/main` then `./dev test` |
| 12:00:53 | a `python3` heredoc writing the PR body in `/tmp` |
| 12:02:39 | `./dev worktree release task/engine-119` |

## Codex session in use

- **`engine ps`** at 11:55:17 listed both Codex rows as `working`:
  `dinoderek/engine#118  mac  Codex  working` and `dinoderek/engine#119
  devbox  Codex  working`. Both came from their windows' `state` in `status`.
- **`engine tell engine-119 "…"`** at 11:55:36 commented on #119 and typed the
  pointer into `engine:@0` on the devbox. The Codex TUI took it as a prompt,
  and the session ran `gh issue view 119 … --json comments` on its next turn.
- **`engine attach engine-119`**, run in a throwaway tmux session, showed the
  devbox's Codex screen over `ssh -t`: the TUI, `GPT-6.1-Sol medium ·
  …/task-engine-119`, and the window `0:engine-119*`. `C-b d` detached it,
  printing `[detached (from session engine)]`, and attach exited 0.
- **The apps.** Both sessions joined engine's daemon. Their processes ran
  `… -C <worktree> … --remote unix://`, under daemons started from engine's
  Codex home with `--remote-control`. The Mac daemon's log reads `remote
  control websocket status changed … next_status=Connected` at 10:22:10, to
  `wss://chatgpt.com/backend-api/wham/remote/control/server` as
  `Dinos-MacBook-Pro.local`, with no later change. Its `session_index.jsonl`
  lists thread `Execute dinoderek/engine#118`, and the devbox's lists `Execute
  dinoderek/engine#119`. No person looked at the apps during the run (Not
  proved).

`status` at 11:57:01: the Mac reported `slots 6, heavy_slots 4, free_slots 3,
free_heavy_slots 3`, for windows `run-engine-74` and `run-engine-116`
(no state, no slot), `engine-95` and `engine-117` (`claude`, `working`), and
`engine-118` (`codex`, `working`). The devbox reported `slots 3, free_slots
2`, for `engine-119` (`codex`, `working`). Both reported `protocol 3` and
55c0f72.

## Objectives of engine#74

| # | Proved by | Not proved |
| --- | --- | --- |
| O1 Codex launch | Live: `engine run` started #118 and #119 through `spawn`, on the hosts' pinned `~/.local/bin/codex` with `hosts.toml`'s launch line, `--dangerously-bypass-hook-trust`, `-C <worktree>` and the kickoff as the first prompt (thread `Execute dinoderek/engine#118`). Both `run.json` files say `"harness": "codex"`. Joined sessions get folder trust from engine's Codex home, not a per-session profile (the engine#74 deviation of 09:52). Tests: `TestSpawnCodexJoins`, `TestSpawnCodexRefusals`, `TestHarnessFromTheCardOrTheFlag` | A refusal, live: none came up |
| O2 session state | Live: `harness-state.json` in both run directories (`working`, `PostToolUse`, with the Codex session id). `status` gave each window `state` and `harness`, and `engine ps` read `working` from it on both hosts. `notify` is unused (`docs/codex.md`). Tests: `TestStateFromRecordedEvents`, `TestCodexRowsReadTheirWindowsState`, `TestRemoteCodexRowsReadTheirWindowsState`, `TestACodexSessionThatWaitsStallsOncePerWait` | A Codex executor seen `waiting` or `ended` by `ps` or `run`: neither session ever waited, and each closed its own window |
| O3 no prompt unattended | Live: 16 requests over both logs, all `left to auto-review`, none denied, and no session stopped on a prompt. Both Codex executors merged with `--match-head-commit` (both commands are in the daemons' session transcripts). Tests: `TestPermissionLeavesAnUnattendedCodexRequestToAutoReview`, `TestMergeGuardReadsCodexShellEvents`, `TestCheckCodexStallsOnAPrompt` | The merge guard blocking a Codex merge, live: no executor tried a bare merge |
| O4 plugin and `--check` | Live: setup item 9 installed the plugin in engine's Codex home on both hosts, and both joined executors ran the engine skill (claim, lock, review, report). `engine start --check --harness codex` passed on both hosts. Tests: `TestCodexPluginRegistersStateHook`, `TestCodexMarketplaceListsThePlugin`, `TestCheckCodex`, `TestCheckCodexFailsWithoutAStateFile` | The plugin in the operator's own `~/.codex`: item 10 is the operator's, still open |
| O5 the apps | Live: both sessions joined their host's engine daemon with remote control on. The Mac daemon was connected to chatgpt.com, and each daemon's thread index lists its session. Tests: `TestSpawnCodexJoinsGated`, `TestSpawnCodexJoinedRecordComesFirst` | Seen in the ChatGPT or Codex app by eye: nobody watched the apps during an unattended run. `start`'s report wrongly said each session was not in the apps (Gaps) |
| O6 placement and setup | Live: #119 (`linux`) on the devbox, #118 (`macos`) on the Mac, two Codex sessions at once, at the cap of 2 across hosts. `engine setup` and `--host devbox` ran items 7 to 11 (Codex installed, signed in, engine's home, the plugin there, the daemon). Tests: `TestTheHarnessCapCountsEveryHost`, `TestACardWhoseHarnessNoHostDefinesWaits`, `internal/setup`'s `TestCodex*` | A Codex task held back by the cap, live: there were only two; `TestTheHarnessCapCountsEveryHost` covers it |
| O7 the fixture | This record | |
| O8 slots | Live: the Mac's `status` gave 6 slots, at most 4 heavy, and two coordinator windows held none (3 free with 3 sessions open). Tests: `TestOnlySessionsHoldSlots`, `TestAHeavyTaskWaitsForAHeavySlotWhileALightOneStarts`, `TestHeavyTasksGoWhereAHeavySlotIsFree`, `internal/hosts` `TestHeavy` | A heavy task, live: the fixture had none |

## Gaps

- **`start` says every Codex session runs embedded.** `engine run`'s log
  printed `Codex apps  not in the ChatGPT or Codex apps: the session runs
  embedded` for both sessions, and both had joined. The line in
  `internal/start` is fixed text for any Codex start. It does not read
  whether `spawn` joined.
- **Setup's preflight waits on the operator's Codex home.** Item 10 is open
  while the plugin is missing from `~/.codex`, and item 12 then does not run.
  Joined sessions never read that home. The preflights were run by hand.
- **A Claude executor skipped the merge lock.** #117 took no `engine:merging`
  and merged 18 seconds after #121, without merging `main` or re-gating. This
  repeats the engine#80 gap. The PRs touched different files, and `main`
  stayed green. #117 also listed as a deviation that its card had no
  `engine:approved`, which a task inside a feature never carries.
- **The reviewer model.** #117's harness refused `claude-opus-5-5` as a
  subagent model, so it used the `opus` alias. The Codex executors' reviewers
  ran on the session's own model.
- **Claims and versions.** #118's claim gives the model as `gpt-6`, though
  `run.json` says `gpt-6.1-sol`. The Mac's TUI is Codex 0.163.0, and both
  daemons and the devbox's TUI are 0.164.0. Setup reports the Mac's 0.163.0
  as ok.
- **The session id.** `run.json` does not record the Codex session id, which
  engine#74's design put there. `harness-state.json` holds it.
- **Small:** each `./dev test`, `git fetch` and worktree release crossed the
  sandbox and went to auto-review, which approved every one. Claim to merge
  took 3 minutes on Codex on the Mac, 7.5 on the devbox (it merged
  `main` in and re-gated), and 3.5 on Claude.
