# Harnesses

> **Owns:** the harness adapter contract, what the core does with an adapter's answers, and the `[harness.<name>]` launch lines and tiers an operator writes. **Not here:** each harness's quirks (the adapter packages' doc comments under `internal/harness/`), host config and placement (`hosts-and-sessions.md`), installing and signing in (`operator-setup.md`).

## The contract

The core knows a harness only through `internal/harness`'s `Adapter`; its
doc comments are the contract. What each answer drives:

| An adapter provides | The core |
| --- | --- |
| a session's launch: config, trust, flags, kickoff | runs the host's line with them; `release` asks every adapter to delete its per-session files |
| whether a session joins the remote-control daemon | joins only when allowed; a down daemon never refuses a spawn |
| one observation per `status`: quota, signed out, window state | unknown is never dead; a window's own exit decides that |
| hook events in engine's terms, its denial, its unattended policy | runs the core hooks: `pre-tool-use` is the merge guard, `permission` applies the policy, `state` writes the run's state file |
| its own hook entries | resolves `engine hook <entry> --harness <name>`; an unknown name or entry exits 1 and blocks nothing |
| `engine setup` steps, preflight failure advice, the user's plugin update | runs the steps after the host's, on hosts that list the harness; `engine upgrade` runs the update on the caller first |
| the preflight kickoff, and whether state comes from hooks alone | fails a check whose window stayed open, skipped a step, or wrote no state file |

What each harness answers today:

| | Claude Code | Codex |
| --- | --- | --- |
| unattended permission request | denied, naming the Permission hard stop | left to Codex's auto-review, which never prompts |
| session state | its own session list | the hooks' state file; a missing file is unknown |

**Adding a harness:** a package under `internal/harness/` that passes
`harnesstest`'s contract suite, a registry line, its `scripts/import-rules.tsv`
row, and its name in the `harness-names` lane. No other package names it.

## Launch lines and tiers

They live only in the operator's `hosts.toml`. `{model}` and `{effort}`
come from the task's tier; `{name}` is `<repo>-<task>`.

```toml
[harness.claude]
max_sessions = 8        # across all hosts: a budget lever
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "claude-opus-5-5",   effort = "high" }
tiers.standard = { model = "claude-sonnet-5-5", effort = "high" }
tiers.light    = { model = "claude-haiku-5-5",  effort = "medium" }

[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }   # not Astra: Sol at high is close on coding, far cheaper
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }
```

Rules for the lines:

- **Claude:** keep `--permission-mode auto`, whose classifier decides most
  actions without a prompt, and `-n {name} --remote-control {name}`, which
  name the session in the apps.
- **Codex:** keep the `workspace-write` sandbox with auto-review deciding
  what crosses it (never `approval_policy = "never"`, which leaves it nothing
  to review), and keep network access, for `gh` and `git push`. Set no
  writable root and no `--add-dir`: a joined session refuses both, so such a
  line keeps every session embedded, and `spawn` warns.
