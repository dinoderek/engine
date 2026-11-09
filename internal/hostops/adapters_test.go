package hostops

import (
	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/claude"
	"github.com/dinoderek/engine/internal/harness/codex"
	"github.com/dinoderek/engine/internal/harness/registry"
)

// The harness names the tests use.
const (
	Claude = claude.Name
	Codex  = codex.Name
)

// Tests that build Deps without adapters get the registry's, built from the
// same Exec, Home, Getenv and Now, as the commands build theirs.
func init() {
	defaultAdapters = func(d Deps) harness.Adapters { return registry.New(envOf(d)) }
}

func envOf(d Deps) harness.Env {
	return harness.Env{Exec: d.Exec, Home: d.Home, Getenv: d.Getenv, Now: d.Now}
}

// withCodex is d with the Codex adapter's app-server reads replaced.
func withCodex(d Deps, opts codex.Options) Deps {
	env := envOf(d)
	d.Harnesses = harness.NewAdapters(claude.New(env), codex.New(env, opts))
	return d
}
