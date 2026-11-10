package ui

import (
	"bytes"
	"net/url"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// md renders CommonMark plus tables. Raw HTML goes through rawText, links
// and images through inert, and nothing parses attribute syntax, so the
// only attributes in the output are the ones this file sets.
var md = goldmark.New(
	goldmark.WithExtensions(extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute))),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(inert{}, 0))),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(rawText{}, 0))),
)

// RenderMarkdown renders a description to HTML the page inserts as markup.
// Raw HTML stays visible as text, only absolute http, https and mailto links
// are live, an image shows only its alt text, and headings start at h3,
// below the panel's own. An empty body renders to "".
func RenderMarkdown(src string) string {
	var out bytes.Buffer
	// Convert fails only when its writer does, and a bytes.Buffer never does.
	_ = md.Convert([]byte(src), &out)
	return out.String()
}

// inert unwraps unsafe links and every image, keeping their text, opens safe
// links in a new tab without a referrer, and moves headings below the panel's.
type inert struct{}

func (inert) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var nodes []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			nodes = append(nodes, n)
		}
		return ast.WalkContinue, nil
	})
	for _, n := range nodes {
		switch n := n.(type) {
		case *ast.Heading:
			n.Level = min(6, n.Level+2)
		case *ast.Image:
			unwrap(n)
		case *ast.Link:
			// The href the renderer would write, entity references resolved.
			if !safeLink(string(util.URLEscape(n.Destination, true))) {
				unwrap(n)
				continue
			}
			live(n)
		case *ast.AutoLink:
			href := string(util.URLEscape(n.URL(source), false))
			if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(href), "mailto:") {
				href = "mailto:" + href
			}
			if !safeLink(href) {
				n.Parent().ReplaceChild(n.Parent(), n, ast.NewString(n.Label(source)))
				continue
			}
			live(n)
		}
	}
}

func live(n ast.Node) {
	n.SetAttributeString("target", []byte("_blank"))
	n.SetAttributeString("rel", []byte("noopener noreferrer"))
}

// unwrap replaces n with its children.
func unwrap(n ast.Node) {
	p := n.Parent()
	for c := n.FirstChild(); c != nil; c = n.FirstChild() {
		n.RemoveChild(n, c)
		p.InsertBefore(p, n, c)
	}
	p.RemoveChild(p, n)
}

// rawText renders raw HTML, inline or as a block, as escaped text.
type rawText struct{}

func (rawText) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindRawHTML, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segs := n.(*ast.RawHTML).Segments
			for i := range segs.Len() {
				seg := segs.At(i)
				_, _ = w.Write(util.EscapeHTML(seg.Value(source)))
			}
		}
		return ast.WalkSkipChildren, nil
	})
	r.Register(ast.KindHTMLBlock, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		b := n.(*ast.HTMLBlock)
		var raw []byte
		for i := range b.Lines().Len() {
			line := b.Lines().At(i)
			raw = append(raw, line.Value(source)...)
		}
		if b.HasClosure() {
			raw = append(raw, b.ClosureLine.Value(source)...)
		}
		_, _ = w.WriteString("<p>")
		_, _ = w.Write(util.EscapeHTML(bytes.TrimRight(raw, "\n")))
		_, _ = w.WriteString("</p>\n")
		return ast.WalkSkipChildren, nil
	})
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
