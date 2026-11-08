package ui

import (
	"net/url"
	"strings"
	"unicode"
)

// Block is one piece of a rendered description. The page builds DOM nodes
// from these and sets every string as text, so source HTML is never markup.
type Block struct {
	Type    string     `json:"type"` // heading, paragraph, list or code
	Level   int        `json:"level,omitempty"`
	Ordered bool       `json:"ordered,omitempty"`
	Inline  []Inline   `json:"inline,omitempty"`
	Items   [][]Inline `json:"items,omitempty"`
	Lang    string     `json:"lang,omitempty"`
	Text    string     `json:"text,omitempty"`
}

// Inline is a run of text, an inline code span, or a link. Href is set only
// for a safe destination; any other link keeps just its text.
type Inline struct {
	Text string `json:"text"`
	Code bool   `json:"code,omitempty"`
	Href string `json:"href,omitempty"`
}

// RenderMarkdown parses the supported Markdown subset: ATX headings, flat
// bullet and numbered lists, fenced code blocks, paragraphs, inline code and
// [text](destination) links. Everything else, raw HTML included, is text.
func RenderMarkdown(src string) []Block {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n"), "\n")
	blocks := []Block{}
	var para []string
	flush := func() {
		if len(para) > 0 {
			blocks = append(blocks, Block{Type: "paragraph", Inline: parseInline(strings.Join(para, " "))})
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fence, lang, ok := openFence(line); ok {
			flush()
			var code []string
			for i++; i < len(lines) && !closesFence(lines[i], fence); i++ {
				code = append(code, lines[i])
			}
			blocks = append(blocks, Block{Type: "code", Lang: lang, Text: strings.Join(code, "\n")})
			continue
		}
		if level, text, ok := heading(line); ok {
			flush()
			blocks = append(blocks, Block{Type: "heading", Level: level, Inline: parseInline(text)})
			continue
		}
		if _, _, ok := listItem(line); ok {
			flush()
			var list Block
			list, i = readList(lines, i)
			blocks = append(blocks, list)
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, strings.TrimSpace(line))
	}
	flush()
	return blocks
}

// readList reads the list starting at lines[i] and returns it with the index
// of its last line. An indented line continues the previous item; a marker of
// the other kind, a blank line or any other text ends the list.
func readList(lines []string, i int) (Block, int) {
	_, ordered, _ := listItem(lines[i])
	list := Block{Type: "list", Ordered: ordered}
	var cur []string
	flush := func() {
		if cur != nil {
			list.Items = append(list.Items, parseInline(strings.Join(cur, " ")))
			cur = nil
		}
	}
	for ; i < len(lines); i++ {
		text, o, ok := listItem(lines[i])
		switch {
		case ok && o == ordered:
			flush()
			cur = []string{text}
		case ok:
			flush()
			return list, i - 1
		case strings.TrimSpace(lines[i]) == "" || !startsIndented(lines[i]) || cur == nil:
			flush()
			return list, i - 1
		default:
			cur = append(cur, strings.TrimSpace(lines[i]))
		}
	}
	flush()
	return list, i - 1
}

func startsIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// openFence recognises a line of three or more backticks or tildes, indented
// by at most three spaces, and returns the fence and the info string's first word.
func openFence(line string) (fence, lang string, ok bool) {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 3 || t[0] != '`' && t[0] != '~' {
		return "", "", false
	}
	n := len(t) - len(strings.TrimLeft(t, t[:1]))
	if n < 3 {
		return "", "", false
	}
	info := strings.TrimSpace(t[n:])
	if t[0] == '`' && strings.Contains(info, "`") {
		return "", "", false
	}
	if f := strings.Fields(info); len(f) > 0 {
		lang = f[0]
	}
	return t[:n], lang, true
}

func closesFence(line, fence string) bool {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return false
	}
	t = strings.TrimRight(t, " \t")
	return len(t) >= len(fence) && strings.Trim(t, fence[:1]) == ""
}

func heading(line string) (int, string, bool) {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return 0, "", false
	}
	n := len(t) - len(strings.TrimLeft(t, "#"))
	if n < 1 || n > 6 || n < len(t) && t[n] != ' ' && t[n] != '\t' {
		return 0, "", false
	}
	text := strings.TrimSpace(t[n:])
	// An optional closing run of # is not part of the heading.
	if trimmed := strings.TrimRight(text, "#"); trimmed != text && (trimmed == "" || strings.HasSuffix(trimmed, " ")) {
		text = strings.TrimSpace(trimmed)
	}
	return n, text, true
}

// listItem recognises "- ", "* ", "+ " and "1. " or "1) " markers at any indent.
func listItem(line string) (text string, ordered, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if len(t) >= 2 && strings.ContainsRune("-*+", rune(t[0])) && (t[1] == ' ' || t[1] == '\t') {
		return strings.TrimSpace(t[2:]), false, true
	}
	d := 0
	for d < len(t) && d < 9 && t[d] >= '0' && t[d] <= '9' {
		d++
	}
	if d > 0 && d+1 < len(t) && (t[d] == '.' || t[d] == ')') && (t[d+1] == ' ' || t[d+1] == '\t') {
		return strings.TrimSpace(t[d+2:]), true, true
	}
	return "", false, false
}

// parseInline splits text into plain runs, code spans and links.
func parseInline(s string) []Inline {
	var out []Inline
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out = append(out, Inline{Text: plain.String()})
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '`':
			if end := strings.IndexByte(s[i+1:], '`'); end > 0 {
				flush()
				out = append(out, Inline{Text: s[i+1 : i+1+end], Code: true})
				i += end + 2
				continue
			}
		case '[':
			if label, dest, n, ok := parseLink(s[i:]); ok {
				flush()
				out = append(out, linkRun(label, dest))
				i += n
				continue
			}
		}
		plain.WriteByte(s[i])
		i++
	}
	flush()
	return out
}

// parseLink reads "[label](destination)" at the start of s, allowing one
// level of balanced parentheses in the destination and an optional title.
func parseLink(s string) (label, dest string, n int, ok bool) {
	end := strings.IndexByte(s, ']')
	if end < 2 || end+1 >= len(s) || s[end+1] != '(' || strings.ContainsAny(s[1:end], "[\n") {
		return "", "", 0, false
	}
	depth := 0
	for j := end + 2; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return s[1:end], destination(s[end+2 : j]), j + 1, true
			}
			depth--
		}
	}
	return "", "", 0, false
}

// destination takes the first word of a link's parenthesised part, dropping
// angle brackets and any title.
func destination(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "<") {
		if end := strings.IndexByte(raw, '>'); end > 0 {
			return raw[1:end]
		}
	}
	if i := strings.IndexFunc(raw, unicode.IsSpace); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func linkRun(label, dest string) Inline {
	run := Inline{Text: label}
	if safeLink(dest) {
		run.Href = dest
	}
	return run
}

// safeLink allows absolute http, https and mailto destinations without
// control characters or spaces. Relative links, fragments and every other
// scheme, javascript: and data: included, are not actionable.
func safeLink(dest string) bool {
	if dest == "" || strings.IndexFunc(dest, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\' }) >= 0 {
		return false
	}
	u, err := url.Parse(dest)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https":
		return u.Host != "" && u.Opaque == ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}
