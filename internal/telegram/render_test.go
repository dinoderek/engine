package telegram

import (
	"math"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func TestQuestionRenderingAndCallbacks(t *testing.T) {
	recommended := 1
	u := control.Update{ID: 42, Item: "t9", Snapshot: &control.Item{Title: "Choose <plain> & text"}, Class: "product", Body: strings.Repeat("界", 1200), Choices: []string{"a", "b"}, Recommended: &recommended}
	text, keys := question(u, "https://dashboard.test")
	for _, want := range []string{"t9 Choose <plain> & text", "Class: product", strings.Repeat("界", 999) + "…", "https://dashboard.test"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, strings.Repeat("界", 1000)) || keys.Rows[1][0].Text != "★ b" || keys.Rows[0][0].Data != "q:42:0" {
		t.Fatalf("rendered %q %+v", text, keys)
	}
	for _, id := range []int{1, 42, math.MaxInt} {
		for index := range 6 {
			data := callbackData(id, index)
			got, choice, ok := parseCallback(data)
			if len(data) > 64 || !ok || got != id || choice != index {
				t.Fatalf("callback %q: %d %d %t", data, got, choice, ok)
			}
		}
	}
	for _, data := range []string{"q:0:1", "q:1:6", "q:1:-1", "q:1:x", "x:1:0", "q:1:0:1", strings.Repeat("x", 65)} {
		if _, _, ok := parseCallback(data); ok {
			t.Fatalf("accepted %q", data)
		}
	}
	if clip("short", 1000) != "short" || heading(control.Update{}) != "" {
		t.Fatal("empty rendering")
	}
	got := digest([]control.TelegramDelivery{{Text: "one"}, {Text: "two", Settled: true}})
	if got != "Engine operator messages\n\none\n\ntwo\nAcknowledged" {
		t.Fatal(got)
	}
}

func TestAFleetQuestionNamesItsHostAndRendersThreeButtons(t *testing.T) {
	zero := 0
	u := control.Update{ID: 7, Host: "mac", Class: "environment", Body: "mac has been unreachable since 2026-11-07 09:00 UTC.",
		Choices: []string{control.ChoiceWait, control.ChoiceMove, control.ChoiceStop}, Recommended: &zero}
	text, keys := question(u, "")
	if !strings.HasPrefix(text, "Host mac\nClass: environment\n\nmac has been unreachable") {
		t.Fatalf("text %q", text)
	}
	var got []string
	for i, row := range keys.Rows {
		if len(row) != 1 || row[0].Data != callbackData(7, i) {
			t.Fatalf("row %d: %+v", i, row)
		}
		got = append(got, row[0].Text)
	}
	if strings.Join(got, "|") != "★ "+control.ChoiceWait+"|"+control.ChoiceMove+"|"+control.ChoiceStop {
		t.Fatalf("buttons %q", got)
	}
	if note := info(control.Update{Host: "mac", Body: "mac answers again"}); note != "Host mac\nmac answers again" {
		t.Fatalf("a note on the host: %q", note)
	}
}

func TestDigestLeavesRoomForSettledMarkers(t *testing.T) {
	var rows []control.TelegramDelivery
	for range 200 {
		rows = append(rows, control.TelegramDelivery{Text: "short message"})
	}
	if digestFits(rows) {
		t.Fatal("oversized digest accepted")
	}
	for !digestFits(rows) {
		rows = rows[:len(rows)-1]
	}
	for i := range rows {
		rows[i].Settled = true
	}
	if len([]rune(digest(rows))) > 3500 {
		t.Fatal("acknowledged digest exceeds reservation")
	}
}
