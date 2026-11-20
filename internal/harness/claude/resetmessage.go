package claude

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// resetPhrase is a time of day after "resets", as a limit message says it:
// "resets 3pm", "resets 3:30pm", "resets at 15:00". A date or weekday
// before the time does not match, since the next such time of day would
// come too soon.
var resetPhrase = regexp.MustCompile(`(?i)\bresets\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap])?\.?m?\b`)

// messageReset is the reset a "resets <time>" in any string of the hook's
// input names: the next occurrence of that time of day after now, in now's
// zone. Zero is unknown.
func messageReset(raw json.RawMessage, now time.Time) int64 {
	var input any
	if json.Unmarshal(raw, &input) != nil {
		return 0
	}
	for _, text := range texts(input) {
		if at, ok := nextResetTime(text, now); ok {
			return at.Unix()
		}
	}
	return 0
}

// texts are the strings in a decoded JSON value, in a stable order.
func texts(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			out = append(out, texts(x)...)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var out []string
		for _, k := range keys {
			out = append(out, texts(v[k])...)
		}
		return out
	}
	return nil
}

// nextResetTime reads the first "resets <time>" in text as the next time
// of day after now, in now's zone.
func nextResetTime(text string, now time.Time) (time.Time, bool) {
	m := resetPhrase.FindStringSubmatch(text)
	if m == nil || m[2] == "" && m[3] == "" {
		return time.Time{}, false
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	switch strings.ToLower(m[3]) {
	case "a", "p":
		if hour < 1 || hour > 12 {
			return time.Time{}, false
		}
		hour %= 12
		if strings.EqualFold(m[3], "p") {
			hour += 12
		}
	default:
		if hour > 23 {
			return time.Time{}, false
		}
	}
	if minute > 59 {
		return time.Time{}, false
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !at.After(now) {
		at = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, now.Location())
	}
	return at, true
}
