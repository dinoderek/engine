# Versions

> **Owns:** where each version is pinned, what the versions lane checks, and the upgrade policy. **Not here:** what the other lanes check (`quality-gates.md`).

## One source per version

| What | Pinned in | Bumped by |
| --- | --- | --- |
| Go toolchain | `go.mod`: the `toolchain` line is the exact Go every gate runs; the `go` line is the language version, the same minor at patch 0 | hand, below |
| golangci-lint, govulncheck | `go.mod`: the `tool` block, with versions in the require lines and sums in `go.sum` | Dependabot, weekly |
| Go modules | `go.mod` and `go.sum` | Dependabot, weekly |
| GitHub Actions | a commit SHA plus a `# vX.Y.Z` comment in `.github/workflows/` | Dependabot, weekly |

Nothing else names a version. `./dev` exports `GOTOOLCHAIN` from the
`toolchain` line, so a newer or older local Go runs exactly the pinned one.
CI's setup-go reads the same line. Docs cite `go.mod`, never the version.

## The versions lane

`./dev test versions` fails when:

- `go.mod` lacks the `go` or `toolchain` line, the two name different minors,
  or the toolchain is older than the language version;
- the gates run on any Go other than the toolchain line (run them through
  `./dev`);
- a workflow sets `go-version` or `GOTOOLCHAIN`, uses setup-go without
  `go-version-file: go.mod`, or pins an action by tag rather than by commit SHA
  with a version comment;
- `.golangci.yml` sets its own Go version;
- a persistent doc (outside `design/`) restates a Go version.

## Upgrade policy

- **Cooldown.** Adopt a release only once it is at least 7 days old.
  Dependabot's cooldown in `.github/dependabot.yml` enforces this for what it
  bumps. Apply it by hand to the toolchain.
- **Go patches.** Move the `toolchain` line to each new patch within a week of
  the cooldown ending. `./dev deps` reports a newer release.
- **Go minors.** Never more than one minor behind the newest. Adopt a new minor
  once its first patch is out and golangci-lint supports it. In one PR, set
  both lines, run `./dev doctor` and `./dev test`, and fix new vet and lint
  findings.
- **Modules, tools and actions.** Dependabot opens one grouped PR per
  ecosystem each week. Patch and minor updates merge on green. For a major
  update, read the release notes and say in the PR what applies to engine. If
  a tool bump adds lint findings, fix them in the same PR. To decline a
  version, add an `ignore` entry to `.github/dependabot.yml` with the reason as
  a comment.
- **Point in time.** `./dev deps` lists outdated direct modules, the tools and
  Go, and runs govulncheck. It fails only on a vulnerability. Fix one by
  upgrading the module that carries it, in its own PR.

Dependabot names the two tool modules in its `allow` list, because their
requirements are marked `// indirect`. If no tool update has arrived for a
month while `./dev deps` reports one, read the Dependabot job log in the repo's
Insights tab and bump the tool by hand with `go get -tool <module>@<version>`.
