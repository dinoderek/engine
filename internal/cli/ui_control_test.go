package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// readTask is a task as the control server holds it now.
func readTask(t *testing.T, s *controlStack, id string) control.Item {
	t.Helper()
	got, err := s.operator().Call(context.Background(), control.Request{Op: "get", ID: id})
	if err != nil || got.Item == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return *got.Item
}

// sheetFits reports whether the open confirmation sheet lies inside the
// viewport, and how far the page scrolls sideways.
type sheetFit struct {
	Left, Right, Top, Bottom, Width, Height float64
	Wide                                    int
}

// controlOnAPhone stops a running task from the plan view at the size of a
// phone: the actions and the confirmation sheet fit the screen with 44px
// targets, nothing is sent until the stop is confirmed, and the confirmed
// stop holds the task on the control server.
func controlOnAPhone(t *testing.T, b *browser, s *controlStack, w *world, shots string) {
	b.viewport(390, 844, true)
	task := w.plansView.ID
	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s'", w.rollout.ID)})
	stop := fmt.Sprintf(`[data-key="act-pane-%s-task-stop"]`, task)
	b.waitFor("the task's stop button", "document.querySelector('"+stop+"') !== null")
	if got := text(b, fmt.Sprintf(`[data-key="act-pane-%s-run"]`, w.rollout.ID)); got != "Run" {
		t.Errorf("the approved plan offers %q, want Run", got)
	}
	if r := tapTargets(b); r.Wide > r.Inner || len(r.Small) > 0 {
		t.Errorf("plan actions at 390x844: page %dpx wide in %dpx; controls under 44px: %s", r.Wide, r.Inner, strings.Join(r.Small, "; "))
	}
	b.screenshot(shots, "phone-actions")

	tap(b, stop)
	b.waitFor("the confirmation sheet", "!document.getElementById('sheet-back').hidden && document.activeElement.id === 'sheet-cancel'")
	sheet := text(b, "#sheet")
	for _, want := range []string{"Stop " + task + "?", "Plans view", "State: Running", "held until you resume it"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the sheet lacks %q: %q", want, sheet)
		}
	}
	var fit sheetFit
	b.value(`(() => { const r = document.getElementById('sheet').getBoundingClientRect();
		return { Left: r.left, Right: r.right, Top: r.top, Bottom: r.bottom, Width: window.innerWidth, Height: window.innerHeight,
			Wide: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) }; })()`, &fit)
	if fit.Left < 0 || fit.Right > fit.Width || fit.Top < 0 || fit.Bottom > fit.Height || fit.Wide > int(fit.Width) {
		t.Errorf("the sheet does not fit the phone: %+v", fit)
	}
	if r := tapTargets(b); len(r.Small) > 0 {
		t.Errorf("the sheet's controls under 44px: %s", strings.Join(r.Small, "; "))
	}
	b.screenshot(shots, "phone-confirm")
	if got := readTask(t, s, task); got.State != "running" || got.Held {
		t.Errorf("opening the sheet changed the task: %+v", got)
	}
	tap(b, "#sheet-cancel")
	b.waitFor("the sheet closed", "document.getElementById('sheet-back').hidden")
	if got := readTask(t, s, task); got.State != "running" || got.Held {
		t.Errorf("cancelling changed the task: %+v", got)
	}

	tap(b, stop)
	b.waitFor("the confirmation sheet again", "!document.getElementById('sheet-back').hidden")
	tap(b, "#sheet-ok")
	b.waitFor("the task held", fmt.Sprintf("document.querySelector('[data-key=\"act-pane-%s-resume\"]') !== null && document.getElementById('sheet-back').hidden", task))
	if got := readTask(t, s, task); got.State != "todo" || !got.Held {
		t.Errorf("the confirmed stop left the task %+v, want todo and held", got)
	}
	if got := text(b, "#task-"+task); !strings.Contains(got, "Stopped; the task is held.") || !strings.Contains(got, "(held)") {
		t.Errorf("the row after the stop: %q", got)
	}
	b.screenshot(shots, "phone-stopped")

	// Resume goes straight through, from the item panel.
	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s?panel=%s'", w.rollout.ID, task)})
	resume := fmt.Sprintf(`[data-key="act-panel-%s-resume"]`, task)
	b.waitFor("the panel's resume button", "document.querySelector('"+resume+"') !== null")
	if r := tapTargets(b); r.Wide > r.Inner || len(r.Small) > 0 {
		t.Errorf("panel actions at 390x844: page %dpx wide in %dpx; controls under 44px: %s", r.Wide, r.Inner, strings.Join(r.Small, "; "))
	}
	tap(b, resume)
	b.waitFor("the resume to land", fmt.Sprintf("document.querySelector('[data-key=\"act-panel-%s-resume\"]') === null", task))
	if got := readTask(t, s, task); got.State != "todo" || got.Held {
		t.Errorf("the resume left the task %+v, want todo and not held", got)
	}
}
