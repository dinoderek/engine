package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

func say(t *testing.T, l *Loop, plan, body string) control.Update {
	t.Helper()
	return command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: body}).Updates[0]
}

func TestFirstStartSendsOpenQuestionsButNoBacklog(t *testing.T) {
	l, f, plan := unpairedLoop(t)
	say(t, l, plan, "stale notice")
	q := ask(t, l, plan)
	say(t, l, plan, "another stale notice")
	pass(t, l, instant)
	pass(t, l, instant.Add(time.Minute))
	sends := f.calls["sendMessage"]
	if len(sends) != 1 || !strings.Contains(sends[0]["text"].(string), "Question") {
		t.Fatalf("first start sent %+v", sends)
	}
	if rows, _, _, _ := deliveries(l.Service.Store); len(rows) != 3 || rows[1].Update != q.ID || rows[1].Message == 0 {
		t.Fatalf("deliveries %+v", rows)
	}
	say(t, l, plan, "fresh notice")
	pass(t, l, instant.Add(2*time.Minute))
	sends = f.calls["sendMessage"]
	if len(sends) != 2 {
		t.Fatalf("a message after start was not digested: %+v", sends)
	}
	text := sends[1]["text"].(string)
	if !strings.Contains(text, "fresh notice") || strings.Contains(text, "stale") {
		t.Fatalf("digest %q", text)
	}
}

func TestFirstStartWithAnEmptyInboxStillDeliversLaterMessages(t *testing.T) {
	l, f, plan := unpairedLoop(t)
	pass(t, l, instant)
	if rows, _, _, _ := l.Service.Store.TelegramState(); len(rows) != 1 || rows[0].Update != 0 || !rows[0].Settled {
		t.Fatalf("no pairing marker: %+v", rows)
	}
	say(t, l, plan, "after pairing")
	pass(t, l, instant.Add(time.Minute))
	if sends := f.calls["sendMessage"]; len(sends) != 1 || !strings.Contains(sends[0]["text"].(string), "after pairing") {
		t.Fatalf("sends %+v", sends)
	}
}
