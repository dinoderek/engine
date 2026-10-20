# Testing strategy

> **Owns:** which test layer a claim belongs in, and the rules every test follows. **Not here:** the coverage target and the lanes that run tests (`quality-gates.md`).

## Layers

| Layer | How | Put a claim here when it is about… |
| --- | --- | --- |
| Unit | a table-driven test of a pure function in the same package | parsing, matching, comparison or rendering, such as a registry parser, trigger patterns or baseline comparison |
| Fake boundary | the code under test gets a fake `repo.Runner` or `tracker.Runner`, or `dev.Tool` fields (`Run`, `LookPath`, `Fetch`, `Attach`) that return canned output | how the code reacts to what git, go, Homebrew or the network say, especially their failures |
| Real process | real `git` or `go` in `t.TempDir()`: a bare origin and a clone, or a small module | that the commands a wrapper runs really do what it claims, such as creating and releasing a worktree, or gofmt flagging a file |
| Command line | `cli.Run` or `dev.Tool.Main` with buffers for stdout and stderr | exit codes, usage text and verb dispatch |

Pick the lowest layer that can prove the claim. Use a real process only when
a fake would assume the very behaviour under test.

## Rules

- **A gate proves it fails.** Every check has a test that feeds it a
  violation and expects the failing exit code and message, plus one that
  passes. A check that cannot be made to fail is a hole.
- **No network, no home directory.** Serve HTTP from `httptest`, and write
  only under `t.TempDir()`.
- **Skip only for a missing tool.** A test that needs `git` or `go` skips when
  the tool is absent. CI has both, so CI never skips.
- **Race detector on.** The `unit` lane runs every test with `-race`. Shared
  state in a test is a bug.
- **No sleeps or wall-clock assertions.** Test durations by rendering a fixed
  value, as the summary test does.
