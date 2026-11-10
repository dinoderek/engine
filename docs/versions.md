# Versions

> **Owns:** where each version is pinned, what the versions lane checks, and the upgrade policy. **Not here:** what the other lanes check (`quality-gates.md`).

## One source per version

| What | Pinned in | Bumped by |
| --- | --- | --- |
| Go toolchain | `go.mod`: the `toolchain` line is the exact Go every gate runs; the `go` line is its minor at patch 0 | hand, below |
| golangci-lint, govulncheck | `go.mod`: the `tool` block | Dependabot, weekly |
| Go modules | `go.mod` and `go.sum` | Dependabot, weekly |
| GitHub Actions | a commit SHA plus a `# vX.Y.Z` comment in `.github/workflows/` | Dependabot, weekly |

Nothing else names a version: `./dev` exports `GOTOOLCHAIN` from the
`toolchain` line, CI's setup-go reads it, and docs cite `go.mod`.

## The versions lane

`./dev test versions` fails when the `go` and `toolchain` lines are missing,
name different minors, or put the toolchain below the language version; when
the gates run on another Go; when a workflow sets its own Go version, uses
setup-go without `go-version-file: go.mod`, or pins an action by tag instead
of a commit SHA with a version comment; when `.golangci.yml` sets a Go version; when a plugin's Claude
and Codex manifests (`plugin.json`) declare different versions; or when a doc
restates a Go version.

## Upgrade policy

- **Cooldown.** Adopt a release only once it is at least 7 days old
  (Dependabot's cooldown enforces this); move the `toolchain` line to each new
  Go patch within a week after.
- **Go minors.** Never more than one minor behind the newest; adopt one once
  its first patch is out and golangci-lint supports it, in one PR that sets
  both lines and fixes new vet and lint findings.
- **Modules, tools and actions.** Dependabot opens one grouped PR per
  ecosystem each week; patch and minor updates merge on green. For a major
  update, say in the PR what the release notes change for engine. Fix lint
  findings a tool bump adds in the same PR. To decline a version, add an
  `ignore` entry to `.github/dependabot.yml` with the reason as a comment.
- **Vulnerabilities.** `./dev deps` reports newer Go and modules and runs
  govulncheck, failing only on a vulnerability; fix it by upgrading the
  module that carries it, in its own PR.

A runtime library a tool also requires, such as the TOML library, takes the
higher of the two versions, so a tool bump can move it: check its release
notes in that PR. Dependabot's `allow` list names the two tool modules,
because their requirements are marked `// indirect`. If a month passes with
no tool update while `./dev deps` reports one, read the Dependabot job log in
Insights, then bump by hand with `go get -tool <module>@<version>`.
