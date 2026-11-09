package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// screenshotsVar names a directory to keep the browser smoke test's
// screenshots in; by default they go to a temporary directory.
const screenshotsVar = "ENGINE_TEST_SCREENSHOTS"

// TestDashboardInBrowser loads the real dashboard, served by `engine ui`
// from the acceptance stack, in a headless Chromium-family browser. It
// drives the page from the keyboard, checks the narrow layout, and takes
// the control server down and up again while an item panel is open.
func TestDashboardInBrowser(t *testing.T) {
	path, ok := findBrowser()
	if !ok {
		t.Skip("no Chromium-family browser is installed")
	}
	s := newControlStack(t, "devbox", "mac")
	w := seedWorld(t, s)
	for name, h := range s.hosts {
		h.set(w.windowsFor(name), nil)
	}
	s.observe()
	d := startDashboard(t, s)
	b := launchBrowser(t, path)
	shots := os.Getenv(screenshotsVar)
	if shots == "" {
		shots = t.TempDir()
	}
	t.Logf("browser %s (%s, sandboxed %t) on %s/%s; screenshots in %s", b.version(), path, sandboxAvailable(), runtime.GOOS, runtime.GOARCH, shots)
	b.viewport(1280, 900, false)
	b.navigate(d.base + "/#/plans")
	var visibility string
	b.value("document.visibilityState", &visibility)
	if visibility != "visible" {
		t.Fatalf("the page is %s; refreshes run only while it is visible", visibility)
	}

	browsePlans(t, b, w, shots)
	readPanels(t, b, w)
	sessionsAndOutage(t, b, s, w, shots)
	narrowLayout(t, b, w, shots)
}

// text is the trimmed text of the first element matching selector.
func text(b *browser, selector string) string {
	b.t.Helper()
	var out string
	b.value(fmt.Sprintf("(document.querySelector(%q) || {}).innerText || ''", selector), &out)
	return strings.TrimSpace(out)
}

func focused(id string) string {
	return fmt.Sprintf("document.activeElement && document.activeElement.id === %q", id)
}

// browsePlans filters the plan list by typing, opens a plan and follows a
// blocker link from the keyboard.
func browsePlans(t *testing.T, b *browser, w *world, shots string) {
	b.waitFor("three plans", "document.querySelectorAll('#plans a').length === 3")
	b.tabTo("#search", 5)
	b.do("Input.insertText", map[string]any{"text": "ROLL"})
	b.waitFor("the search to leave one plan", "document.querySelectorAll('#plans a').length === 1")
	b.tabTo(fmt.Sprintf(`a[data-key="plan-%s"]`, w.rollout.ID), 5)
	b.press("Enter")
	b.waitFor("the rollout task pane", fmt.Sprintf("document.querySelectorAll('#pane tbody tr').length === 6 && location.hash.startsWith('#/plans/%s')", w.rollout.ID))
	var counts []string
	b.value("[...document.querySelectorAll('#pane .count')].map((n) => n.innerText.trim())", &counts)
	if strings.Join(counts, ",") != "1 Done,2 In progress,1 Ready to run,2 Blocked" {
		t.Errorf("task counts %v", counts)
	}
	if got := text(b, "#pane h2"); !strings.HasPrefix(got, "Tasks · "+w.rollout.ID+" · Blocked") {
		t.Errorf("pane heading %q", got)
	}
	for id, host := range map[string]string{w.plansView.ID: "devbox", w.sessionsUI.ID: "mac", w.docs.ID: "—"} {
		if got := text(b, "#task-"+id+` td[data-label="Host"]`); got != host {
			t.Errorf("host of %s: %q, want %q", id, got, host)
		}
	}
	if got := text(b, "#task-"+w.links.ID+" .feedback a"); got != "Needs feedback" {
		t.Errorf("the task table's feedback link: %q", got)
	}
	b.screenshot(shots, "plans-wide")
	b.tabTo(fmt.Sprintf(`a[data-key="dep-%s-%s"]`, w.accept.ID, w.audit.ID), 60)
	b.press("Enter")
	b.waitFor("the blocker's plan, hidden by the filter", fmt.Sprintf("location.hash.startsWith('#/plans/%s/%s') && document.querySelector('#task-%s.selected') !== null", w.backup.ID, w.audit.ID, w.audit.ID))
	if got := text(b, "#plans"); !strings.Contains(got, "Dashboard rollout") || strings.Contains(got, "Backup restore drill") {
		t.Errorf("the filter must still apply to the list: %q", got)
	}
}

// readPanels opens task and plan panels from the keyboard: tabs by arrow
// keys, Escape closing with focus returned, Markdown rendered safely, and
// thread paging and direct links to one message.
func readPanels(t *testing.T, b *browser, w *world) {
	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s'", w.rollout.ID)})
	opener := fmt.Sprintf(`a[data-key="open-%s"]`, w.links.ID)
	b.waitFor("the rollout pane", "document.querySelector('"+opener+"') !== null")
	b.tabTo(opener, 60)
	b.press("Enter")
	b.waitFor("the links panel", "!document.getElementById('panel').hidden && "+focused("panel-title")+" && document.getElementById('panel-title').innerText === 'Feedback links'")
	b.tabTo("#tab-description", 5)
	b.press("ArrowRight")
	b.waitFor("the communications tab", focused("tab-communications")+" && document.querySelectorAll('#messages li').length === 3")
	if got := text(b, "#update-"+fmt.Sprint(w.linksQuestion)+" .question"); got != "Open · acknowledged · 1 reply" {
		t.Errorf("question badge %q", got)
	}
	b.press("Escape")
	b.waitFor("focus back on the opener", "document.getElementById('panel').hidden && document.activeElement.dataset.key === 'open-"+w.links.ID+"'")

	b.tabTo(fmt.Sprintf(`a[data-key="open-%s"]`, w.rollout.ID), 60)
	b.press("Enter")
	b.waitFor("the plan description", "document.querySelector('#view-description pre code') !== null")
	var md struct {
		Heading, Code, Text string
		Items, Scripts      int
		Links               []string
	}
	b.value(`(() => { const v = document.getElementById('view-description'); return {
		Heading: v.querySelector('h1, h2, h3, h4, h5, h6').innerText, Code: v.querySelector('pre code').innerText, Text: v.innerText,
		Items: v.querySelectorAll('ul li').length, Scripts: v.querySelectorAll('script').length,
		Links: [...v.querySelectorAll('a')].map((a) => a.getAttribute('href')) }; })()`, &md)
	if md.Heading != "Outcome" || strings.TrimSpace(md.Code) != "engine ui" || md.Items != 2 || md.Scripts != 0 ||
		strings.Join(md.Links, " ") != "https://example.test/control" || !strings.Contains(md.Text, "<script>alert(1)</script>") {
		t.Errorf("rendered description: %+v", md)
	}
	b.press("Escape")

	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s?panel=%s&tab=communications'", w.rollout.ID, w.sessionsUI.ID)})
	b.waitFor("the first page of 50", "document.querySelectorAll('#messages li').length === 50 && !document.getElementById('more').hidden")
	b.tabTo("#more", 80)
	b.press("Enter")
	b.waitFor("the rest of the thread", fmt.Sprintf("document.querySelectorAll('#messages li').length === %d && document.getElementById('more').hidden", progressSays))
	b.press("Escape")
	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s?panel=%s&tab=communications&update=%d'", w.rollout.ID, w.sessionsUI.ID, w.lastProgress)})
	b.waitFor("the linked message, beyond the first page", focused(fmt.Sprintf("update-%d", w.lastProgress)))
	b.press("Escape")
}

// sessionsAndOutage opens a feedback request from Sessions, then takes the
// control server down and brings it back while that panel stays open.
func sessionsAndOutage(t *testing.T, b *browser, s *controlStack, w *world, shots string) {
	b.do("Runtime.evaluate", map[string]any{"expression": "location.hash = '#/sessions'"})
	b.waitFor("six sessions", "document.querySelectorAll('#sessions tbody tr').length === 6")
	var order []string
	b.value("[...document.querySelectorAll('#sessions tbody tr')].map((r) => r.id)", &order)
	want := []string{"session-" + w.links.ID, "session-" + w.hardening.ID, "session-" + w.rotate.ID}
	if strings.Join(order[:3], ",") != strings.Join(want, ",") {
		t.Errorf("feedback requests must lead: %v", order)
	}
	if got := text(b, "#tab-sessions"); got != "Sessions · 6" {
		t.Errorf("tab %q", got)
	}
	pv := "#session-" + w.plansView.ID
	if text(b, pv+" .observed") != "Waiting" || text(b, pv+" .feedback") != "" || !strings.Contains(text(b, pv+` td[data-label="Latest attached update"]`), "Waiting for the harness") {
		t.Errorf("an ordinary wait must show as waiting, without feedback: %q", text(b, pv))
	}
	if got := text(b, "#session-"+w.rotate.ID+" .feedback"); got != "Needs feedback · plan "+w.hardening.ID {
		t.Errorf("the parent plan's question: %q", got)
	}
	b.tabTo(fmt.Sprintf(`a[data-key="s-fb-%s"]`, w.links.ID), 40)
	b.press("Enter")
	question := fmt.Sprintf("update-%d", w.linksQuestion)
	b.waitFor("the question over Sessions", focused(question)+" && !document.getElementById('sessions').hidden && location.hash.startsWith('#/sessions?')")
	b.screenshot(shots, "sessions-question")

	s.stop()
	b.do("Runtime.evaluate", map[string]any{"expression": "document.dispatchEvent(new Event('visibilitychange'))"})
	b.waitFor("stale marks", "document.getElementById('status').classList.contains('stale') && !document.getElementById('panel-banner').hidden && document.querySelector('#sessions .banner') !== null")
	if got := text(b, "#status"); !strings.Contains(got, "control server unreachable") {
		t.Errorf("status during the outage: %q", got)
	}
	b.waitFor("the open panel and its focus kept", focused(question)+" && document.querySelectorAll('#messages li').length === 3 && document.querySelectorAll('#sessions tbody tr').length === 6")
	b.screenshot(shots, "outage-stale")

	s.start()
	b.do("Runtime.evaluate", map[string]any{"expression": "document.dispatchEvent(new Event('visibilitychange'))"})
	b.waitFor("recovery", "!document.getElementById('status').classList.contains('stale') && document.getElementById('panel-banner').hidden && document.querySelector('#sessions .banner') === null")
	b.waitFor("the panel still open on its message", focused(question)+" && document.getElementById('panel-title').innerText === 'Feedback links'")
	if got := text(b, "#status"); !strings.HasPrefix(got, "Last refresh") {
		t.Errorf("status after recovery: %q", got)
	}
	b.screenshot(shots, "recovered")
	b.press("Escape")
}

// narrowLayout checks the stacked layout on a phone-sized screen, with the
// panel over the page and the page behind it inert.
func narrowLayout(t *testing.T, b *browser, w *world, shots string) {
	b.viewport(390, 844, true)
	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s'", w.rollout.ID)})
	b.waitFor("the stacked task pane", "document.querySelectorAll('#pane tbody tr').length === 6 && getComputedStyle(document.querySelector('#pane thead')).display === 'none'")
	var layout struct {
		Columns  string
		Overflow bool
		Label    string
	}
	b.value(`({ Columns: getComputedStyle(document.getElementById('workspace')).gridTemplateColumns,
		Overflow: document.documentElement.scrollWidth > window.innerWidth,
		Label: getComputedStyle(document.querySelector('#pane td[data-label="Host"]'), '::before').content })`, &layout)
	if strings.Contains(strings.TrimSpace(layout.Columns), " ") || layout.Overflow || !strings.Contains(layout.Label, "Host") {
		t.Errorf("narrow layout: %+v", layout)
	}
	b.screenshot(shots, "plans-narrow")
	opener := fmt.Sprintf(`a[data-key="open-%s"]`, w.links.ID)
	b.tabTo(opener, 60)
	b.press("Enter")
	b.waitFor("the panel over the page", "getComputedStyle(document.getElementById('panel')).position === 'fixed' && document.getElementById('bar').inert && document.getElementById('pane').inert && "+
		focused("panel-title")+" && document.getElementById('panel-title').innerText === 'Feedback links'")
	b.screenshot(shots, "panel-narrow")
	b.press("Escape")
	b.waitFor("the page usable again", "!document.getElementById('pane').inert && document.activeElement.dataset.key === 'open-"+w.links.ID+"'")
}
