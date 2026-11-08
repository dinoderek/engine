package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func text(s string) []Inline { return []Inline{{Text: s}} }

func TestRenderMarkdownBlocks(t *testing.T) {
	src := "## Outcome\n\nAn operator opens a panel,\nthen reads it.\n\n- one\n- two\n  continues here\n* three\n\n1. first\n2) second\n\n```go title\nfunc main() {}\n\n# not a heading\n```\n\n# Last ##\n"
	want := []Block{
		{Type: "heading", Level: 2, Inline: text("Outcome")},
		{Type: "paragraph", Inline: text("An operator opens a panel, then reads it.")},
		{Type: "list", Items: [][]Inline{text("one"), text("two continues here"), text("three")}},
		{Type: "list", Ordered: true, Items: [][]Inline{text("first"), text("second")}},
		{Type: "code", Lang: "go", Text: "func main() {}\n\n# not a heading"},
		{Type: "heading", Level: 1, Inline: text("Last")},
	}
	if got := RenderMarkdown(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %+v\nwant     %+v", got, want)
	}
}

func TestRenderMarkdownEdges(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []Block
	}{
		{"missing body", "", []Block{}},
		{"blank body", " \n\t\n", []Block{}},
		{"crlf", "# A\r\n\r\ntext\r\n", []Block{{Type: "heading", Level: 1, Inline: text("A")}, {Type: "paragraph", Inline: text("text")}}},
		{"hash without space", "#hashtag and C#", []Block{{Type: "paragraph", Inline: text("#hashtag and C#")}}},
		{"seven hashes", "####### x", []Block{{Type: "paragraph", Inline: text("####### x")}}},
		{"heading closer kept in word", "## C#", []Block{{Type: "heading", Level: 2, Inline: text("C#")}}},
		{"empty heading", "#\n", []Block{{Type: "heading", Level: 1}}},
		{"unterminated fence runs to the end", "~~~\nraw <b>\n- x", []Block{{Type: "code", Text: "raw <b>\n- x"}}},
		{"short fence does not close a long one", "````\n```\n````", []Block{{Type: "code", Text: "```"}}},
		{"fence indented four is text", "    ```\n", []Block{{Type: "paragraph", Inline: text("```")}}},
		{"list ends at other kind", "- a\n1. b", []Block{{Type: "list", Items: [][]Inline{text("a")}}, {Type: "list", Ordered: true, Items: [][]Inline{text("b")}}}},
		{"list ends at text", "- a\nplain", []Block{{Type: "list", Items: [][]Inline{text("a")}}, {Type: "paragraph", Inline: text("plain")}}},
		{"list ends at blank", "- a\n\n  indented paragraph", []Block{{Type: "list", Items: [][]Inline{text("a")}}, {Type: "paragraph", Inline: text("indented paragraph")}}},
		{"marker needs a space", "-a\n1.b\n12345678901. c", []Block{{Type: "paragraph", Inline: text("-a 1.b 12345678901. c")}}},
	} {
		if got := RenderMarkdown(tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: blocks = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestBacktickFenceTakesNoBacktickInInfo(t *testing.T) {
	if got := RenderMarkdown("``` a`b\ntext"); len(got) != 1 || got[0].Type != "paragraph" {
		t.Fatalf("blocks = %+v, want one paragraph, not a code block", got)
	}
}

func TestRenderMarkdownKeepsRawHTMLAsText(t *testing.T) {
	src := "<script>alert(1)</script> and <img src=x onerror=alert(2)>\n\n- <b>bold</b>\n\n```html\n<iframe src=\"//evil\"></iframe>\n```"
	blocks := RenderMarkdown(src)
	if got := blocks[0].Inline[0].Text; got != "<script>alert(1)</script> and <img src=x onerror=alert(2)>" {
		t.Fatalf("paragraph text = %q, want the HTML verbatim as text", got)
	}
	if got := blocks[1].Items[0][0].Text; got != "<b>bold</b>" {
		t.Fatalf("list item = %q, want the HTML verbatim as text", got)
	}
	if blocks[2].Text != `<iframe src="//evil"></iframe>` || blocks[2].Lang != "html" {
		t.Fatalf("code = %+v, want the HTML verbatim", blocks[2])
	}
	b, _ := json.Marshal(blocks)
	if strings.Contains(string(b), "<") {
		t.Fatalf("encoded blocks contain a raw <: %s", b)
	}
}

func TestRenderMarkdownInline(t *testing.T) {
	got := parseInline("run `./dev test` then [the docs](https://example.com/a_(b) \"title\") or [x](<https://example.com/y z>) `open")
	want := []Inline{
		{Text: "run "}, {Text: "./dev test", Code: true}, {Text: " then "},
		{Text: "the docs", Href: "https://example.com/a_(b)"}, {Text: " or "},
		{Text: "x"}, {Text: " `open"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inline = %+v\nwant     %+v", got, want)
	}
	for _, src := range []string{"[open](https://x", "[a [b]](https://x)", "[](https://x)", "[a] (https://x)", "[a]"} {
		for _, run := range parseInline(src) {
			if run.Href != "" {
				t.Errorf("%q produced a link: %+v", src, run)
			}
		}
	}
}

func TestSafeLinkDestinations(t *testing.T) {
	for dest, want := range map[string]bool{
		"https://example.com/a?b=c#d":     true,
		"HTTP://Example.com":              true,
		"mailto:ops@example.com":          true,
		"javascript:alert(1)":             false,
		"JaVaScRiPt:alert(1)":             false,
		"java\tscript:alert(1)":           false,
		"java\x00script:alert(1)":         false,
		" javascript:alert(1)":            false,
		"data:text/html,<script>":         false,
		"vbscript:x":                      false,
		"file:///etc/passwd":              false,
		"ftp://example.com":               false,
		"//example.com/x":                 false,
		"/relative/path":                  false,
		"docs/readme.md":                  false,
		"#fragment":                       false,
		"https:example.com":               false,
		"https://":                        false,
		"https://exa mple.com":            false,
		"https://example.com\\@evil.test": false,
		"mailto:":                         false,
		"":                                false,
		"http://[::1":                     false,
	} {
		if got := safeLink(dest); got != want {
			t.Errorf("safeLink(%q) = %v, want %v", dest, got, want)
		}
	}
}

func TestUnsafeLinkKeepsOnlyItsText(t *testing.T) {
	got := parseInline("[click me](javascript:alert(1)) or [docs](docs/x.md)")
	want := []Inline{{Text: "click me"}, {Text: " or "}, {Text: "docs"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inline = %+v, want %+v", got, want)
	}
}
