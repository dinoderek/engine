package claude

import (
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
)

func TestContract(t *testing.T) {
	harnesstest.Contract(t, func(env harness.Env) harness.Adapter { return New(env) })
}
