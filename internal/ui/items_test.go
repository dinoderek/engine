package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

const hostile = "# Plan\n\n<script>alert(1)</script> [go](javascript:alert(2)) and [docs](https://example.com/d)\n\n- <img src=x onerror=alert(3)>\n"

// itemFake serves item reads and per-item updates.
func itemFake(t *testing.T) *fakeControl {
	fc := newFakeControl(t)
	items := map[string]control.Item{
		"p5": {ID: "p5", Kind: "plan", Repo: "r1", Title: "Hostile", State: "planned", Body: hostile},
		"t6": {ID: "t6", Kind: "task", Repo: "r1", Plan: "p5", Title: "No body", State: "todo"},
	}
	updates := []control.Update{ask(1, "p5"), upd(2, "t6", "say", "<b>hi</b>"), upd(3, "p5", "say", "plan note"), upd(4, "t6", "state", "running")}
	fc.reads = func(r control.Request) (control.Response, bool) {
		switch r.Op {
		case "get":
			if it, ok := items[r.ID]; ok {
				return control.Response{Item: &it}, true
			}
			return control.Response{}, false
		case "updates":
			var out control.Response
			for _, u := range updates {
				if u.Item == r.ID && u.ID > r.After {
					out.Updates = append(out.Updates, u)
				}
			}
			return out, true
		}
		return control.Response{}, false
	}
	return fc
}

func TestItemRouteRendersTheDescription(t *testing.T) {
	d := newDashboard(itemFake(t).client())
	res, body := d.get(t, "GET", "127.0.0.1:7332", "/api/items/p5")
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("GET item = %d %v", res.StatusCode, res.Header)
	}
	var env struct{ Data ItemDetail }
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	want := "<h3>Plan</h3>\n" +
		"<p>&lt;script&gt;alert(1)&lt;/script&gt; [go](javascript:alert(2)) and [docs](https://example.com/d)</p>\n" +
		"<ul>\n<li>\n<p>&lt;img src=x onerror=alert(3)&gt;</p>\n</li>\n</ul>\n"
	if got := env.Data.DescriptionHTML; got != want {
		t.Fatalf("description = %q\nwant          %q", got, want)
	}
	_, body = d.get(t, "GET", "127.0.0.1:7332", "/api/items/t6")
	if !strings.Contains(body, `"description_html":""`) || !strings.Contains(body, `"plan":"p5"`) {
		t.Fatalf("task without a body = %s, want an empty description", body)
	}
}

func TestItemRoutesRefuseOtherItems(t *testing.T) {
	fc := itemFake(t)
	d := newDashboard(fc.client())
	for _, target := range []string{"/api/items/r1", "/api/items/a4", "/api/items/t", "/api/items/t0", "/api/items/t1x", "/api/items/r1/thread", "/api/items/..%2Fp5"} {
		res, body := d.get(t, "GET", "127.0.0.1", target)
		if res.StatusCode != 404 {
			t.Errorf("GET %s = %d %s, want 404", target, res.StatusCode, body)
		}
	}
	if calls := fc.calls(); len(calls) != 0 {
		t.Fatalf("refused items still made calls %v", calls)
	}
	res, body := d.get(t, "GET", "127.0.0.1", "/api/items/t99")
	if env := decode(t, body); res.StatusCode != 404 || env.Error == "" {
		t.Fatalf("GET unknown item = %d %s, want 404", res.StatusCode, body)
	}
}

func TestThreadRouteIsScopedAndPaged(t *testing.T) {
	fc := itemFake(t)
	d := newDashboard(fc.client())
	_, body := d.get(t, "GET", "127.0.0.1", "/api/items/t6/thread")
	if !strings.Contains(body, `"item":"t6"`) || !strings.Contains(body, `"id":2`) || strings.Contains(body, "plan note") || strings.Contains(body, "running") {
		t.Fatalf("task thread = %s, want only the task's message", body)
	}
	var env struct{ Data Thread }
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Data.Messages[0].Body != "<b>hi</b>" || strings.Contains(body, "<") {
		t.Fatalf("task thread = %s, want the message as inert text", body)
	}
	_, body = d.get(t, "GET", "127.0.0.1", "/api/items/p5/thread?after=1&anchor=3")
	if !strings.Contains(body, "plan note") || strings.Contains(body, "Approve?") || !strings.Contains(body, `"questions":[{"id":1,"state":"open"}]`) {
		t.Fatalf("plan thread after 1 = %s, want the later message and the question's status", body)
	}
	if _, body = d.get(t, "GET", "127.0.0.1", "/api/items/t6/thread?after=2"); !strings.Contains(body, `"messages":[]`) || !strings.Contains(body, `"next":2`) {
		t.Fatalf("thread at its end = %s, want no messages and the cursor kept", body)
	}
	for _, ops := range fc.calls() {
		if ops != "get" && ops != "updates" {
			t.Fatalf("thread reads forwarded %v, want only get and updates", fc.calls())
		}
	}
}

func TestThreadRouteRejectsBadCursors(t *testing.T) {
	fc := itemFake(t)
	d := newDashboard(fc.client())
	for _, q := range []string{"?after=-1", "?after=x", "?anchor=1.5", "?anchor=99999999999", "?after="} {
		res, body := d.get(t, "GET", "127.0.0.1", "/api/items/t6/thread"+q)
		want := 400
		if q == "?after=" {
			want = 200
		}
		if res.StatusCode != want {
			t.Errorf("GET thread%s = %d %s, want %d", q, res.StatusCode, body, want)
		}
	}
}

func TestItemRoutesReportAnUnreachableControlServer(t *testing.T) {
	fc := itemFake(t)
	d := newDashboard(fc.client())
	fc.failWith(503)
	for _, target := range []string{"/api/items/p5", "/api/items/p5/thread"} {
		res, body := d.get(t, "GET", "127.0.0.1", target)
		if env := decode(t, body); res.StatusCode != 502 || env.Data != nil || env.Error == "" {
			t.Errorf("GET %s while down = %d %s, want a 502 with an error and no data", target, res.StatusCode, body)
		}
	}
}
