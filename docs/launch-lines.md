# Launch lines and tiers

> **Owns:** the `[harness.<name>]` launch lines and tiers an operator writes in `hosts.toml`, and the rules for them. **Not here:** the adapter contract (`harnesses.md`), the hosts and placement (`hosts-and-sessions.md`).

They live only in the operator's `hosts.toml`. `{model}` and `{effort}` come
from the task's tier; `{name}` is `<repo>-<task>`; `{title}` is the app title.

```toml
[harness.claude]
max_sessions = 8        # across all hosts: a budget lever
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {title} --remote-control {title}"
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
  actions without a prompt, and `-n {title} --remote-control {title}`.
- **Codex:** keep the `workspace-write` sandbox with auto-review deciding
  what crosses it (never `approval_policy = "never"`, which leaves it nothing
  to review), and keep network access, for `gh` and `git push`. Set no
  writable root and no `--add-dir`: a joined session refuses both, so such a
  line keeps sessions embedded, and `spawn` warns.
