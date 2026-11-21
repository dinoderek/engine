package telegram

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/control"
)

type button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type keyboard struct {
	Rows [][]button `json:"inline_keyboard"`
}

func clip(text string, limit int) string {
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit-1]) + "…"
}

func callbackData(id, index int) string { return fmt.Sprintf("q:%d:%d", id, index) }
func parseCallback(data string) (int, int, bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "q" || len(data) > 64 {
		return 0, 0, false
	}
	id, e1 := strconv.Atoi(parts[1])
	index, e2 := strconv.Atoi(parts[2])
	return id, index, e1 == nil && e2 == nil && id > 0 && index >= 0 && index < 6
}

func heading(u control.Update) string {
	title := ""
	if u.Snapshot != nil {
		title = u.Snapshot.Title
	}
	return strings.TrimSpace(u.Item + " " + clip(title, 180))
}
func question(u control.Update, dashboard string) (string, keyboard) {
	text := heading(u)
	if u.Class != "" {
		text += "\nClass: " + u.Class
	}
	text += "\n\n" + clip(u.Body, 1000) + "\n\n" + dashboard
	keys := keyboard{Rows: [][]button{}}
	for i, choice := range u.Choices {
		if u.Recommended != nil && *u.Recommended == i {
			choice = "★ " + choice
		}
		keys.Rows = append(keys.Rows, []button{{choice, callbackData(u.ID, i)}})
	}
	return text, keys
}
func info(u control.Update) string { return heading(u) + "\n" + clip(u.Body, 500) }
func digest(rows []control.TelegramDelivery) string {
	text := "Engine operator messages"
	for _, d := range rows {
		text += "\n\n" + d.Text
		if d.Settled {
			text += "\nAcknowledged"
		}
	}
	return text
}

// Reserve room for every eventual acknowledgement when grouping a digest.
func digestFits(rows []control.TelegramDelivery) bool {
	size := len([]rune(digest(rows)))
	for _, d := range rows {
		if !d.Settled {
			size += len("\nAcknowledged")
		}
	}
	return size <= 3500
}
