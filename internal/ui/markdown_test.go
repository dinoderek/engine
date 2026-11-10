package ui

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderMarkdownFeatures(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"empty", "", ""},
		{"blank", " \n\t\n", ""},
		{"headings start at h3", "# A\n\n## B\n\n##### E\n", "<h3>A</h3>\n<h4>B</h4>\n<h6>E</h6>\n"},
		{"setext heading", "A\n===\n", "<h3>A</h3>\n"},
		{"emphasis and code", "**b** *i* `x < y`", "<p><strong>b</strong> <em>i</em> <code>x &lt; y</code></p>\n"},
		{"nested lists", "- a\n  - b\n    1. c\n", "<ul>\n<li>a\n<ul>\n<li>b\n<ol>\n<li>c</li>\n</ol>\n</li>\n</ul>\n</li>\n</ul>\n"},
		{"ordered start", "3. c\n", "<ol start=\"3\">\n<li>c</li>\n</ol>\n"},
		{"fenced code", "```go title\nfunc main() {}\n```\n", "<pre><code class=\"language-go\">func main() {}\n</code></pre>\n"},
		{"indented code", "    a\n", "<pre><code>a\n</code></pre>\n"},
		{"blockquote and rule", "> q\n\n---\n", "<blockquote>\n<p>q</p>\n</blockquote>\n<hr>\n"},
		{"table with alignment", "| a | b | c |\n|:-|:-:|-:|\n| 1 | 2 | 3 |\n",
			"<table>\n<thead>\n<tr>\n<th align=\"left\">a</th>\n<th align=\"center\">b</th>\n<th align=\"right\">c</th>\n</tr>\n</thead>\n" +
				"<tbody>\n<tr>\n<td align=\"left\">1</td>\n<td align=\"center\">2</td>\n<td align=\"right\">3</td>\n</tr>\n</tbody>\n</table>\n"},
		{"safe links open in a new tab", "[docs](https://example.com/a_(b) \"T\") <mailto:ops@example.com> <ops@example.com>",
			"<p><a href=\"https://example.com/a_(b)\" title=\"T\" target=\"_blank\" rel=\"noopener noreferrer\">docs</a> " +
				"<a href=\"mailto:ops@example.com\" target=\"_blank\" rel=\"noopener noreferrer\">mailto:ops@example.com</a> " +
				"<a href=\"mailto:ops@example.com\" target=\"_blank\" rel=\"noopener noreferrer\">ops@example.com</a></p>\n"},
		{"reference link", "[docs][d]\n\n[d]: https://example.com\n", "<p><a href=\"https://example.com\" target=\"_blank\" rel=\"noopener noreferrer\">docs</a></p>\n"},
		{"relative link keeps its text", "[readme](docs/x.md) and [top](#top)", "<p>readme and top</p>\n"},
		{"image keeps only its alt text", "![a *chart*](https://example.com/c.png)", "<p>a <em>chart</em></p>\n"},
		{"linked image", "[![logo](https://example.com/l.png)](https://example.com)", "<p><a href=\"https://example.com\" target=\"_blank\" rel=\"noopener noreferrer\">logo</a></p>\n"},
	} {
		if got := RenderMarkdown(tc.src); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestRenderMarkdownShowsRawHTMLAsText(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"script block", "<script>alert(1)</script> and [x](https://example.com)", "<p>&lt;script&gt;alert(1)&lt;/script&gt; and [x](https://example.com)</p>\n"},
		{"inline tag", "a <b onclick=\"x()\">b</b>", "<p>a &lt;b onclick=&quot;x()&quot;&gt;b&lt;/b&gt;</p>\n"},
		{"multi-line block", "<div>\n<img src=x onerror=alert(1)>\n</div>\n", "<p>&lt;div&gt;\n&lt;img src=x onerror=alert(1)&gt;\n&lt;/div&gt;</p>\n"},
		{"comment", "<!-- hidden -->", "<p>&lt;!-- hidden --&gt;</p>\n"},
		{"html in a code span", "`<script>x</script>`", "<p><code>&lt;script&gt;x&lt;/script&gt;</code></p>\n"},
		{"html in a fence", "```html\n<iframe src=\"//evil\"></iframe>\n```", "<pre><code class=\"language-html\">&lt;iframe src=&quot;//evil&quot;&gt;&lt;/iframe&gt;\n</code></pre>\n"},
		{"entities stay text", "&lt;script&gt; &#60;b&#62;", "<p>&lt;script&gt; &lt;b&gt;</p>\n"},
	} {
		got := RenderMarkdown(tc.src)
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
		assertInert(t, tc.name, got)
	}
}

// hostile are descriptions written to get markup, script or a live unsafe
// link into the page.
var hostileDescriptions = map[string]string{
	"script":              "<script>alert(1)</script>",
	"unclosed script":     "<script src=//evil.test/x.js",
	"split script":        "<scr<script>ipt>alert(1)</script>",
	"style":               "<style>body{display:none}</style>",
	"iframe":              "text <iframe src=javascript:alert(1)></iframe>",
	"svg onload":          "<svg/onload=alert(1)>",
	"img onerror":         "x <img src=x onerror=alert(1)>",
	"event in a link":     "<a href=\"https://example.com\" onmouseover=\"alert(1)\">x</a>",
	"javascript link":     "[x](javascript:alert(1))",
	"mixed case scheme":   "[x](JaVaScRiPt:alert(1))",
	"entity in scheme":    "[x](&#106;avascript:alert(1)) [y](java&#x09;script:alert(1))",
	"escaped colon":       "[x](javascript&#58;alert(1))",
	"percent scheme":      "[x](javascript%3Aalert(1))",
	"data link":           "[x](data:text/html;base64,PHNjcmlwdD4=)",
	"vbscript link":       "[x](vbscript:msgbox(1))",
	"file link":           "[x](file:///etc/passwd)",
	"angle destination":   "[x](<javascript:alert(1)>)",
	"autolinks":           "<javascript:alert(1)> <data:text/html,x> <vbscript:x>",
	"reference":           "[x][r]\n\n[r]: javascript:alert(1)\n",
	"image source":        "![x](javascript:alert(1)) ![y](data:image/svg+xml,<svg/onload=alert(1)>) ![z](https://evil.test/p.gif)",
	"title breakout":      "[x](https://example.com \"\\\" onmouseover=\\\"alert(1)\")",
	"autolink breakout":   "<https://example.com/\"onmouseover=\"alert(1)>",
	"fence info breakout": "```x\" onclick=\"alert(1)\n<b>\n```",
	"attribute syntax":    "# Title {onclick=alert(1)}\n\npara{#id .c onclick=x}",
	"nested links":        "[[inner](javascript:alert(1))](https://example.com) [![i](javascript:1)](javascript:2)",
	"link in a table":     "| a |\n|---|\n| [x](javascript:alert(1)) <b>c</b> |\n",
	"html in a list":      "- <img src=x onerror=alert(1)>\n  - <script>alert(2)</script>",
	"cdata and pi":        "<![CDATA[<script>]]> <?php echo 1 ?> <!DOCTYPE html>",
	"nul byte":            "<scr\x00ipt>alert(1)</script> [x](java\x00script:1)",
	"deep quote":          strings.Repeat(">", 2000) + " x",
	"unclosed brackets":   strings.Repeat("[a](", 5000),
	"deep list":           strings.Repeat("- ", 500) + "x",
}

func TestRenderMarkdownKeepsHostileInputInert(t *testing.T) {
	for name, src := range hostileDescriptions {
		assertInert(t, name, RenderMarkdown(src))
	}
}

// The checker itself must catch each way markup could go live.
func TestAssertInertFailsOnLiveMarkup(t *testing.T) {
	for _, html := range []string{
		"<script>x</script>", "<p onclick=\"x\">a</p>", `<a href="javascript:x">a</a>`, `<a href="/rel">a</a>`,
		`<img src="https://example.com/i.png">`, "<!-- c -->", "<p>a > b</p>", `<code class="x">a</code>`, `<p title="t">a</p>`,
	} {
		if inertProblem(html) == "" {
			t.Errorf("inertProblem(%q) passed live markup", html)
		}
	}
	if p := inertProblem(RenderMarkdown("# A\n\n[x](https://example.com) `c`\n\n```go\nx\n```\n\n| a |\n|:-:|\n| b |\n\n3. x\n")); p != "" {
		t.Errorf("inertProblem refused safe output: %s", p)
	}
}

func assertInert(t *testing.T, name, html string) {
	t.Helper()
	if p := inertProblem(html); p != "" {
		t.Errorf("%s: %s in %.300q", name, p, html)
	}
}

var (
	tagRE  = regexp.MustCompile(`^<(/?)([a-z0-9]+)((?: [a-z]+="[^"<>]*")*)>`)
	attrRE = regexp.MustCompile(` ([a-z]+)="([^"]*)"`)
	// allowed is every element the renderer may emit, with the attributes
	// each may carry and a pattern for each attribute's value.
	allowed = map[string]map[string]*regexp.Regexp{
		"p": nil, "h3": nil, "h4": nil, "h5": nil, "h6": nil, "ul": nil, "li": nil, "pre": nil, "em": nil, "strong": nil,
		"blockquote": nil, "hr": nil, "br": nil, "table": nil, "thead": nil, "tbody": nil, "tr": nil,
		"ol":   {"start": regexp.MustCompile(`^[0-9]+$`)},
		"code": {"class": regexp.MustCompile(`^language-[^ ]+$`)},
		"th":   {"align": regexp.MustCompile(`^(left|center|right)$`)},
		"td":   {"align": regexp.MustCompile(`^(left|center|right)$`)},
		"a": {
			"href":   regexp.MustCompile(`^(https?://[^/]|mailto:.)`),
			"title":  regexp.MustCompile(``),
			"target": regexp.MustCompile(`^_blank$`),
			"rel":    regexp.MustCompile(`^noopener noreferrer$`),
		},
	}
)

// inertProblem returns what makes html unsafe to insert, or "" when every
// tag is an allowed element with allowed attributes, every link is an
// absolute http, https or mailto link, and no other markup character
// appears outside a tag.
func inertProblem(html string) string {
	for i := 0; i < len(html); {
		switch html[i] {
		case '>', '"':
			return "a bare " + html[i:i+1]
		case '<':
			m := tagRE.FindStringSubmatch(html[i:])
			if m == nil {
				return "markup that is not a plain tag"
			}
			attrs, ok := allowed[m[2]]
			if !ok {
				return "element " + m[2]
			}
			for _, a := range attrRE.FindAllStringSubmatch(m[3], -1) {
				if re := attrs[a[1]]; re == nil || !re.MatchString(a[2]) || m[1] == "/" {
					return "attribute " + a[0] + " on " + m[2]
				}
			}
			if m[2] == "a" && m[1] == "" && !strings.Contains(m[3], ` href="`) {
				return "a link without a destination"
			}
			i += len(m[0])
		default:
			i++
		}
	}
	return ""
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
