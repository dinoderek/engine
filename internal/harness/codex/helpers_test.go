package codex

import (
	"os"
	"path/filepath"
	"testing"
)

// failedCodexTurn is a stored turn that failed on the usage limit.
const failedCodexTurn = `{"data":[{"id":"turn-a","status":"failed","startedAt":900,"completedAt":1000,"error":{"codexErrorInfo":"usageLimitExceeded"}}]}`

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
