package hostops

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCodexTurnsUsesReadOnlyRequestsAndKeepsPartialResults(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	write(t, bin, `#!/bin/sh
[ "$1 $2 $3" = 'app-server --listen stdio://' ] || exit 2
[ "$CODEX_HOME" = "$0.home" ] || exit 3
read -r init
printf '%s\n' '{"id":1,"result":{}}'
read -r initialized
read -r first
case "$first" in *'"thread/turns/list"'*'"itemsView":"notLoaded"'*) : ;; *) exit 4 ;; esac
printf '%s\n' '{"id":2,"error":{"code":-32602}}'
read -r second
case "$second" in *'"thread/turns/list"'*) : ;; *) exit 5 ;; esac
printf '%s\n' '{"id":3,"result":{"data":[{"status":"failed"}]}}'
while read -r line; do :; done
`, 0755)
	ctx, cancel := context.WithTimeout(context.Background(), codexQuotaTimeout)
	defer cancel()
	got, err := readCodexTurns(ctx, bin, bin+".home", []string{"missing", "present"})
	if err != nil || len(got) != 1 || !json.Valid(got["present"]) {
		t.Fatalf("turns %s, %v", got["present"], err)
	}
}

func TestReadCodexTurnsTimesOut(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	write(t, bin, "#!/bin/sh\nwhile read -r line; do :; done\n", 0755)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := readCodexTurns(ctx, bin, t.TempDir(), []string{"a"}); err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("err %v, ctx %v", err, ctx.Err())
	}
}
