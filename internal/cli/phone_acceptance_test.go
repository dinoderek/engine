package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/telegram"
	"github.com/dinoderek/engine/internal/ui"
)

// The phone's names: the dashboard's tailnet name, the operator's Telegram
// chat and the dashboard link alerts carry.
const (
	tailnetHost  = "phone.tailnet.test"
	operatorChat = 4242
	dashboardURL = "https://" + tailnetHost
)

// phone is the acceptance fixture: a control server with one coordinating
// plan whose task's session runs on devbox, `engine ui --host` in front of
// it, a browser that reaches the dashboard by its tailnet name, and a fake
// Bot API behind the Telegram loop engine serve runs.
type phone struct {
	t     *testing.T
	s     *controlStack
	d     *dashboard
	b     *browser
	tg    *fakeTelegram
	shots string
	// origin is the dashboard as the phone reaches it.
	origin           string
	plan, task       control.Item
	worker           control.Client
	tokenFile        string
	first, second    int // the session's questions
	message          int // the session's message to the operator
	stopFirstLoop    func()
	firstQ, secondQ  int64 // their Telegram messages
	digest           int64 // the operator messages' digest
	recommendedFirst int
}

// TestOperateEngineFromAPhone proves the operator can run engine from a phone
// end to end: questions with choices from a session's CLI, Telegram alerts
// and one-tap answers, the dashboard on the tailnet with its writes guarded,
// the inbox at a phone's size, and plan and task control with confirmations.
func TestOperateEngineFromAPhone(t *testing.T) {
	path, ok := findBrowser()
	if !ok {
		t.Skip("no Chromium-family browser is installed")
	}
	p := newPhone(t, path)
	for _, g := range []struct {
		name string
		run  func(*phone)
	}{
		{"O1 questions carry choices, a recommendation and a class", (*phone).questionsCarryChoices},
		{"O3 skills offer choices", (*phone).skillsOfferChoices},
		{"O4 Telegram alerts on every new operator question", (*phone).telegramAlerts},
		{"O5 a tap in Telegram answers the question", (*phone).tapAnswers},
		{"O2 a settled question cannot be settled again", (*phone).settledOnce},
		{"O6 the dashboard serves the tailnet itself", (*phone).servesTheTailnet},
		{"O7 the dashboard writes safely", (*phone).writesSafely},
		{"O8 the inbox works on a phone", (*phone).inboxOnAPhone},
		{"O5 Telegram follows answers given elsewhere", (*phone).telegramFollows},
		{"O9 every dashboard view renders well on a phone", (*phone).viewsOnAPhone},
		{"O10 plan and task control works from the dashboard", (*phone).controlFromAPhone},
	} {
		if !t.Run(g.name, func(t *testing.T) { p.t, p.b.t, p.s.t = t, t, t; g.run(p) }) {
			return
		}
	}
}

func newPhone(t *testing.T, browserPath string) *phone {
	s := newControlStack(t, "devbox")
	p := &phone{t: t, s: s, tg: newFakeTelegram(t), shots: os.Getenv(screenshotsVar)}
	if p.shots == "" {
		p.shots = t.TempDir()
	}
	sd := &seeder{t: t, s: s, op: s.operator(), w: &world{attempts: map[string]control.Attempt{}, windows: map[string]string{}}, workers: map[string]control.Client{}}
	repo := sd.repo("Alpha", "owner/alpha")
	p.plan = sd.plan(repo, "Phone rollout", "## Outcome\n\nOperate from a phone.\n")
	p.task = sd.task(p.plan, "Docs pass", "## Outcome\n\nDocs.\n")
	sd.call(sd.op, control.Request{Op: "approve", ID: p.plan.ID, Version: p.plan.Version})
	sd.state(sd.op, p.plan.ID, "planned")
	sd.call(sd.op, control.Request{Op: "run", ID: p.plan.ID})
	sd.run(p.task, "devbox", "@4")
	p.worker = sd.workers[p.task.ID]
	p.tokenFile = filepath.Join(t.TempDir(), "server-token")
	writeFile(t, p.tokenFile, p.worker.Token+"\n")
	s.hosts["devbox"].set([]hostops.Window{{Name: fmt.Sprintf("alpha-%d", control.Number(p.task.ID)), ID: "@4", Alive: true, State: session.StateWaiting}}, nil)
	s.observe()

	p.d = startDashboard(t, s, "--host", tailnetHost)
	p.origin = "http://" + tailnetHost + p.d.base[strings.LastIndex(p.d.base, ":"):]
	p.b = launchBrowser(t, browserPath, "--host-resolver-rules=MAP "+tailnetHost+" 127.0.0.1")
	return p
}

// session runs an engine command as the task's session runs it: its own
// process, holding only the execution credential the launch wrote.
func (p *phone) session(args ...string) (int, string) {
	self, err := os.Executable()
	if err != nil {
		p.t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Env = append(processEnv(), "HOME="+p.t.TempDir(), engineProcessVar+"=1",
		"ENGINE_SERVER_URL="+p.s.url(), "ENGINE_SERVER_TOKEN_FILE="+p.tokenFile)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		p.t.Fatal(err)
	}
	return cmd.ProcessState.ExitCode(), out.String()
}

// ask asks a question from the session's CLI and returns its update number.
func (p *phone) ask(text, class string, recommend int, choices ...string) int {
	args := []string{"ask", p.task.ID, text, "--class", class, "--recommend", fmt.Sprint(recommend)}
	for _, c := range choices {
		args = append(args, "--choice", c)
	}
	code, out := p.session(args...)
	var got control.Response
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Updates) != 1 {
		p.t.Fatalf("engine ask: exit %d: %s", code, out)
	}
	return got.Updates[0].ID
}

// updates is every update on the task, as the operator reads them.
func (p *phone) updates() []control.Update {
	got, err := p.s.operator().Call(context.Background(), control.Request{Op: "updates", ID: p.task.ID})
	if err != nil {
		p.t.Fatal(err)
	}
	return got.Updates
}

// answers are the bodies of every update that answers update n with kind.
func (p *phone) answers(kind string, n int) []string {
	var out []string
	for _, u := range p.updates() {
		if u.Kind == kind && u.ReplyTo == n {
			out = append(out, u.Body)
		}
	}
	return out
}

// question finds update n in a read's updates.
func question(us []control.Update, n int) (control.Update, bool) {
	for _, u := range us {
		if u.ID == n {
			return u, true
		}
	}
	return control.Update{}, false
}

func (p *phone) questionsCarryChoices() {
	t := p.t
	p.recommendedFirst = 1
	p.first = p.ask("Merge the docs pass?", "review", 1, "Hold", "Merge now", "Close the task")
	inbox, err := p.s.operator().Call(context.Background(), control.Request{Op: "inbox"})
	if err != nil {
		t.Fatal(err)
	}
	own, err := p.worker.Call(context.Background(), control.Request{Op: "updates", ID: p.task.ID})
	if err != nil {
		t.Fatal(err)
	}
	for name, us := range map[string][]control.Update{"updates": p.updates(), "inbox": inbox.Updates, "the session's updates": own.Updates} {
		q, ok := question(us, p.first)
		check(t, ok && q.RequiresReply && strings.Join(q.Choices, "|") == "Hold|Merge now|Close the task" &&
			q.Recommended != nil && *q.Recommended == 1 && q.Class == "review", "%s: the question reads %+v", name, q)
	}
	var list ui.InboxList
	p.d.get(t, "/api/inbox", &list)
	check(t, len(list.Cards) == 1 && list.Cards[0].Class == "review" && len(list.Cards[0].Choices) == 3 &&
		list.Cards[0].Recommended != nil && *list.Cards[0].Recommended == 1, "the dashboard's card: %+v", list.Cards)

	before := len(p.updates())
	code, out := p.session("ask", p.task.ID, "Out of range?", "--choice", "Yes", "--choice", "No", "--recommend", "2", "--class", "review")
	check(t, code != 0 && strings.Contains(out, "recommended 2 is not a choice"), "an out-of-range recommendation: exit %d: %s", code, out)
	for _, r := range []control.Request{
		{Op: "ask", ID: p.task.ID, Body: "Seven?", Choices: []string{"a", "b", "c", "d", "e", "f", "g"}},
		{Op: "ask", ID: p.task.ID, Body: "Long?", Choices: []string{strings.Repeat("x", control.ChoiceRunes+1)}},
		{Op: "ask", ID: p.task.ID, Body: "Class?", Class: "whim"},
	} {
		var prob *control.Problem
		_, err := p.worker.Call(context.Background(), r)
		check(t, errors.As(err, &prob) && prob.Status == 400, "%s: %v, want a 400", r.Body, err)
	}
	check(t, len(p.updates()) == before, "refused questions wrote updates")
	check(t, readTask(t, p.s, p.task.ID).State == "needs-human", "the question holds the task in needs-human")
}

func (p *phone) skillsOfferChoices() {
	raw, err := os.ReadFile(filepath.Join("..", "..", "plugins", "engine", "skills", "build", "references", "hard-stops.md"))
	if err != nil {
		p.t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	for _, want := range []string{"Always pass `--class`", "for a pick, pass each option as a `--choice`", "`--recommend <index from 0>`"} {
		check(p.t, strings.Contains(text, want), "the build skill's hard stops lack %q", want)
	}
}

// eventually polls cond against a deadline: the Telegram loop and the page
// act on their own schedules.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s: it never happened", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// buttons are a sent message's buttons: each one's text and callback data.
func buttons(c botCall) []string {
	var out []string
	markup, _ := c.Body["reply_markup"].(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	for _, row := range rows {
		for _, b := range row.([]any) {
			m := b.(map[string]any)
			out = append(out, fmt.Sprintf("%v=%v", m["text"], m["callback_data"]))
		}
	}
	return out
}

func (p *phone) telegramAlerts() {
	t := p.t
	say, err := p.worker.Call(context.Background(), control.Request{Op: "say", ID: p.task.ID, Body: "Docs are green and waiting.", Recipient: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	p.message = say.Updates[0].ID
	p.stopFirstLoop = p.tg.loop(p.s.service, operatorChat, dashboardURL)
	eventually(t, "the alert and the digest", func() bool { return len(p.tg.sent("sendMessage")) >= 2 })
	sends := p.tg.sent("sendMessage")
	alert, digest := sends[0], sends[1]
	p.firstQ, p.digest = p.tg.message(0), p.tg.message(1)
	text, _ := alert.Body["text"].(string)
	for _, want := range []string{p.task.ID + " Docs pass", "Class: review", "Merge the docs pass?", dashboardURL} {
		check(t, strings.Contains(text, want), "the alert lacks %q: %q", want, text)
	}
	q := fmt.Sprintf("q:%d:", p.first)
	check(t, strings.Join(buttons(alert), " ") == "Hold="+q+"0 ★ Merge now="+q+"1 Close the task="+q+"2", "the alert's buttons: %v", buttons(alert))
	check(t, alert.Body["disable_notification"] == false && alert.Body["chat_id"] == float64(operatorChat), "the alert is audible, to the chat: %v", alert.Body)
	dtext, _ := digest.Body["text"].(string)
	check(t, strings.Contains(dtext, "Docs are green and waiting.") && len(buttons(digest)) == 0 && digest.Body["disable_notification"] == true,
		"the message goes as a silent digest: %v", digest.Body)

	// A restart neither resends nor skips: the server reopens its store, and
	// the question asked before the new loop starts is the only send after it.
	p.stopFirstLoop()
	p.s.stop()
	p.s.start()
	p.second = p.ask("Is the wording right?", "product", 0, "Keep it", "Shorten it")
	p.tg.loop(p.s.service, operatorChat, dashboardURL)
	eventually(t, "the second alert", func() bool { return len(p.tg.sent("sendMessage")) >= 3 })
	sends = p.tg.sent("sendMessage")
	p.secondQ = p.tg.message(2)
	text, _ = sends[2].Body["text"].(string)
	check(t, len(sends) == 3 && strings.Contains(text, "Is the wording right?") && strings.Contains(text, "Class: product"),
		"after the restart: %d sends, the last %q", len(sends), text)

	// With no chat or token configured, the server starts no Telegram work.
	var logged []string
	idle := telegram.Configured(t.TempDir(), p.s.service, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	done := make(chan struct{})
	go func() { defer close(done); idle(context.Background()) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("an unconfigured Telegram loop kept running")
	}
	check(t, len(logged) == 0, "an unconfigured server logged %v", logged)
}

func (p *phone) tapAnswers() {
	t := p.t
	p.tg.tap("tap-stranger", 777, p.secondQ, fmt.Sprintf("q:%d:0", p.second))
	data := fmt.Sprintf("q:%d:%d", p.first, p.recommendedFirst)
	p.tg.tap("tap-1", operatorChat, p.firstQ, data)
	p.tg.tap("tap-1", operatorChat, p.firstQ, data)
	eventually(t, "both deliveries of the tap answered", func() bool { return len(p.tg.sent("answerCallbackQuery")) >= 2 })
	for _, c := range p.tg.sent("answerCallbackQuery") {
		check(t, c.Body["callback_query_id"] == "tap-1" && c.Body["text"] == "Answered: Merge now", "the tap's answer: %v", c.Body)
	}
	got := p.answers("resolved", p.first)
	check(t, len(got) == 1 && got[0] == "Merge now", "a redelivered tap resolved %q, want Merge now once", got)
	edits := p.tg.edits(p.firstQ)
	check(t, len(edits) > 0 && strings.HasSuffix(edits[len(edits)-1], "\n\nAnswered: Merge now"), "the tapped message's edits: %q", edits)
	check(t, len(p.answers("resolved", p.second)) == 0 && len(p.tg.edits(p.secondQ)) == 0, "a tap from another chat changed the second question")
}

// post sends a dashboard write to host, from origin, with contentType.
func (p *phone) post(host, origin, contentType, path string, body any) (int, ui.Envelope) {
	p.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		p.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, p.d.base+path, bytes.NewReader(raw))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var env ui.Envelope
	_ = json.NewDecoder(res.Body).Decode(&env)
	return res.StatusCode, env
}

func (p *phone) settledOnce() {
	before := len(p.updates())
	loopback := strings.TrimPrefix(p.d.base, "http://")
	status, env := p.post(loopback, p.d.base, "application/json", fmt.Sprintf("/api/updates/%d/resolve", p.first), map[string]any{"key": "again", "choice": 0})
	by := 0
	for _, u := range p.updates() {
		if u.Kind == "resolved" && u.ReplyTo == p.first {
			by = u.ID
		}
	}
	want := fmt.Sprintf("question %d is already resolved, by update %d", p.first, by)
	check(p.t, status == 409 && env.Reason == control.ReasonAlreadyResolved && strings.Contains(env.Error, want), "resolving again: %d %+v", status, env)
	check(p.t, len(p.updates()) == before, "the refused resolve wrote an update")
}

func (p *phone) servesTheTailnet() {
	t := p.t
	for host, want := range map[string]int{tailnetHost + p.origin[strings.LastIndex(p.origin, ":"):]: 200, "localhost": 200, "elsewhere.test": 421} {
		req, err := http.NewRequest(http.MethodGet, p.d.base+"/api/inbox", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		check(t, res.StatusCode == want, "GET /api/inbox for %s: %d, want %d", host, res.StatusCode, want)
	}
	// The badge calls land where the test can read them.
	p.b.do("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": "window.badge = null;" +
		"navigator.setAppBadge = (n) => { window.badge = n; return Promise.resolve(); };" +
		"navigator.clearAppBadge = () => { window.badge = 0; return Promise.resolve(); };"})
	p.b.viewport(390, 844, true)
	p.b.navigate(p.origin + "/#/inbox")
	p.b.waitFor("the inbox over the tailnet name", "document.querySelectorAll('#cards li').length === 2")
}

func (p *phone) writesSafely() {
	t := p.t
	host := strings.TrimPrefix(p.origin, "http://")
	path := "/api/items/" + p.task.ID + "/say"
	before := len(p.updates())
	for _, r := range []struct {
		name, origin, kind, path string
		body                     any
		want                     int
	}{
		{"a foreign origin", "https://evil.example", "application/json", path, map[string]any{"key": "x", "body": "hi"}, 403},
		{"no origin", "", "application/json", path, map[string]any{"key": "x", "body": "hi"}, 403},
		{"an opaque origin", "null", "application/json", path, map[string]any{"key": "x", "body": "hi"}, 403},
		{"a form post", p.origin, "text/plain", path, map[string]any{"key": "x", "body": "hi"}, 415},
		{"an unknown field", p.origin, "application/json", path, map[string]any{"key": "x", "body": "hi", "op": "delete"}, 400},
		{"an op off the allowlist", p.origin, "application/json", "/api/tasks/" + p.task.ID + "/edit", map[string]any{"key": "x"}, 404},
		{"a read as a write", p.origin, "application/json", "/api/inbox", map[string]any{"key": "x"}, 405},
		{"an item the server does not have", p.origin, "application/json", "/api/items/t999/say", map[string]any{"key": "x", "body": "hi"}, 404},
	} {
		status, _ := p.post(host, r.origin, r.kind, r.path, r.body)
		check(t, status == r.want, "%s: %d, want %d", r.name, status, r.want)
	}
	check(t, len(p.updates()) == before, "a refused write reached the server")

	body := map[string]any{"key": "say-from-phone", "body": "Shorter, please."}
	s1, e1 := p.post(host, p.origin, "application/json", path, body)
	s2, e2 := p.post(host, p.origin, "application/json", path, body)
	check(t, s1 == 200 && s2 == 200 && fmt.Sprint(e1.Data) == fmt.Sprint(e2.Data), "a resent say: %d %+v, %d %+v", s1, e1, s2, e2)
	said := 0
	for _, u := range p.updates() {
		if u.Kind == "say" && u.Body == "Shorter, please." {
			said++
		}
	}
	check(t, said == 1, "the resent say applied %d times", said)
}
