# Seed prompt (ephemeral design)

Designed, not built. A prompt, or a retrofit skill, that applies the project
context guideline (`docs/project-context.md`) to a repo: it picks the tools
for each stack. When it ships, give it a persistent home and delete this.

## Modes

- **New repo:** interview the operator (stack, deploy target, stateful
  infrastructure, UI, product decisions); generate the core plus the modules
  that apply, with the docs check, lanes, CI and dependency bots; open one PR.
- **Retrofit:** audit an existing repo against this guideline and report the
  gaps as a plan with tasks, ready for the protocol.
