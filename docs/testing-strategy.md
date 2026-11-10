# Testing strategy

> **Owns:** which test layer a claim belongs in, and the rules every test follows. **Not here:** the coverage target and the lanes that run tests (`quality-gates.md`).

## Layers

| Layer | How | Put a claim here when it is about… |
| --- | --- | --- |
| Unit | a table-driven test of a pure function | parsing, matching, comparison or rendering |
| Fake boundary | a fake `repo.Runner`, `session.RunFunc` or `dev.Tool` field returns canned output | how the code reacts to what git, go, Homebrew or the network say, especially their failures |
| Real process | real `git`, `go`, `sqlite3`, `tmux`, `node` or a Chromium browser in `t.TempDir()` or on a private socket (`tmux -L <unique name>`, killed afterwards) | actual storage transactions, worktrees, formatting or session operations |
| Command line | `cli.Run` or `dev.Tool.Main` with buffers | exit codes, usage text and verb dispatch |

Pick the lowest layer that can prove the claim; use a real process only when
a fake would assume the behaviour under test.

## Rules

- **A gate proves it fails.** Every check has a test that feeds it a
  violation and expects the failing exit code and message, and one that
  passes.
- **No network, no home directory.** Use `httptest`, and write only under
  `t.TempDir()`.
- **Skip only for a missing tool.** A test that needs `git`, `go`, `sqlite3`,
  `tmux`, `node` or a Chromium browser skips when it is absent; CI installs
  the missing tools and requires a browser, so CI never skips. A tmux test
  never touches the default server, and never runs `caffeinate`.
- **Race detector on:** the `unit` lane runs with `-race`.
- **No sleeps or wall-clock assertions.** Test durations by rendering a fixed
  value. A real-process test may poll, against a deadline, for another
  process's effect; it never asserts on a duration.
