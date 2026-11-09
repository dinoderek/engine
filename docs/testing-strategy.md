# Testing strategy

> **Owns:** which test layer a claim belongs in, and the rules every test follows. **Not here:** the coverage target and the lanes that run tests (`quality-gates.md`).

## Layers

| Layer | How | Put a claim here when it is about… |
| --- | --- | --- |
| Unit | a table-driven test of a pure function in the same package | parsing, matching, comparison or rendering, such as a registry parser, trigger patterns or baseline comparison |
| Fake boundary | the code under test gets a fake `repo.Runner` or `session.RunFunc`, or `dev.Tool` fields (`Run`, `LookPath`, `Fetch`, `Attach`) that return canned output | how the code reacts to what git, go, Homebrew or the network say, especially their failures |
| Real process | real `git`, `go`, `sqlite3`, `tmux`, `node` or a Chromium browser in `t.TempDir()` or on a private socket (`tmux -L <unique name>`, killed afterwards) | actual storage transactions, worktrees, formatting or session operations |
| Command line | `cli.Run` or `dev.Tool.Main` with buffers for stdout and stderr | exit codes, usage text and verb dispatch |

Pick the lowest layer that can prove the claim. Use a real process only when
a fake would assume the very behaviour under test.

## Rules

- **A gate proves it fails.** Every check has a test that feeds it a
  violation and expects the failing exit code and message, plus one that
  passes. A check that cannot be made to fail is a hole.
- **No network, no home directory.** Serve HTTP from `httptest`, and write
  only under `t.TempDir()`.
- **Skip only for a missing tool.** A test that needs `git`, `go`, `sqlite3`, `tmux`, `node`
  or a Chromium browser skips when the tool is absent. CI installs missing tools (including
  tmux and node when the runner lacks them) and requires a browser, so CI never skips. A tmux test never touches the default
  server, and never runs `caffeinate`.
- **Race detector on.** The `unit` lane runs every test with `-race`. Shared
  state in a test is a bug.
- **No sleeps or wall-clock assertions.** Test durations by rendering a fixed
  value, as the summary test does. A real-process test may poll, against a
  deadline, for another process's effect, such as a log filling; it never
  asserts on a duration.
