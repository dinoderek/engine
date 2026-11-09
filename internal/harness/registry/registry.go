// Package registry is the one list of the harness adapters engine knows. It
// is the only package that imports a concrete adapter; the commands build
// their adapters here and hand them to the core.
package registry

import (
	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/claude"
	"github.com/dinoderek/engine/internal/harness/codex"
)

// New is every adapter, reading and running through env. Claude is the
// default: a request that names no harness launches it.
func New(env harness.Env) harness.Adapters {
	return harness.NewAdapters(claude.New(env), codex.New(env, codex.Options{}))
}
