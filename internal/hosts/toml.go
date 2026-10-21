package hosts

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Error is a violation in a host config file, with the 1-based line it is on.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// kind is the type of a parsed value.
type kind int

const (
	kString kind = iota
	kInt
	kArray
	kTable
)

func (k kind) String() string {
	return [...]string{"a string", "an integer", "an array of strings", "a table"}[k]
}

// origin records how a table came to exist, which decides whether a later
// header may reopen it: only a table made implicitly, as the parent of a
// header such as [a.b], can be defined once by its own header.
type origin int

const (
	implicit origin = iota
	explicit
	dotted
	inline
)

// node is one parsed value, with the line its key starts on.
type node struct {
	line    int
	kind    kind
	str     string
	num     int
	items   []string
	entries map[string]*node
	order   []string
	origin  origin
}

func newTable(line int, o origin) *node {
	return &node{line: line, kind: kTable, entries: map[string]*node{}, origin: o}
}

func (n *node) add(key string, v *node) {
	n.entries[key] = v
	n.order = append(n.order, key)
}

var integer = regexp.MustCompile(`^[+-]?(0|[1-9][0-9]*)$`)

func isBareKeyChar(c byte) bool {
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// parse reads src, the TOML subset host config uses: tables, dotted keys,
// strings (single-line, basic or literal), integers, arrays of strings,
// one-line inline tables and comments. Anything else is an *Error.
func parse(src string) (root *node, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*Error)
			if !ok {
				panic(r)
			}
			root, err = nil, e
		}
	}()
	if !utf8.ValidString(src) {
		panic(&Error{Line: 1, Msg: "file is not valid UTF-8"})
	}
	p := &parser{src: src, line: 1}
	p.root = newTable(1, explicit)
	p.cur = p.root
	p.document()
	return p.root, nil
}

type parser struct {
	src  string
	pos  int
	line int
	root *node
	cur  *node
}

func (p *parser) fail(format string, args ...any) {
	p.failAt(p.line, format, args...)
}

func (p *parser) failAt(line int, format string, args ...any) {
	panic(&Error{Line: line, Msg: fmt.Sprintf(format, args...)})
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) has(prefix string) bool { return strings.HasPrefix(p.src[p.pos:], prefix) }

func (p *parser) skipSpaces() {
	for p.peek() == ' ' || p.peek() == '\t' {
		p.pos++
	}
}

func (p *parser) skipComment() {
	if p.peek() != '#' {
		return
	}
	for !p.eof() && p.peek() != '\n' {
		p.pos++
	}
}

// skipBlank skips whitespace, newlines and comments.
func (p *parser) skipBlank() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\r':
			p.pos++
		case '\n':
			p.pos++
			p.line++
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

// endOfLine requires that nothing but a comment follows on the line.
func (p *parser) endOfLine() {
	p.skipSpaces()
	p.skipComment()
	if p.peek() == '\r' {
		p.pos++
	}
	switch {
	case p.eof():
	case p.peek() == '\n':
		p.pos++
		p.line++
	default:
		p.fail("unexpected %q after the value", p.peek())
	}
}

func (p *parser) document() {
	for {
		p.skipBlank()
		if p.eof() {
			return
		}
		if p.peek() == '[' {
			p.header()
		} else {
			p.keyValue(p.cur)
		}
		p.endOfLine()
	}
}

// keyPath reads a bare, possibly dotted key.
func (p *parser) keyPath() []string {
	var path []string
	for {
		p.skipSpaces()
		if c := p.peek(); c == '"' || c == '\'' {
			p.fail("quoted keys are not supported")
		}
		start := p.pos
		for !p.eof() && isBareKeyChar(p.peek()) {
			p.pos++
		}
		if p.pos == start {
			p.fail("expected a key, found %s", p.describe())
		}
		path = append(path, p.src[start:p.pos])
		p.skipSpaces()
		if p.peek() != '.' {
			return path
		}
		p.pos++
	}
}

func (p *parser) describe() string {
	switch {
	case p.eof():
		return "the end of the file"
	case p.peek() == '\n' || p.peek() == '\r':
		return "the end of the line"
	}
	return strconv.QuoteRune(rune(p.peek()))
}

func (p *parser) expect(c byte) {
	if p.peek() != c {
		p.fail("expected %q, found %s", c, p.describe())
	}
	p.pos++
}

// header handles a [table] line, creating the table and making it current.
func (p *parser) header() {
	p.pos++
	if p.peek() == '[' {
		p.fail("arrays of tables are not supported")
	}
	line := p.line
	path := p.keyPath()
	p.skipSpaces()
	p.expect(']')
	t := p.root
	for i, key := range path {
		child := t.entries[key]
		last := i == len(path)-1
		switch {
		case child == nil && last:
			child = newTable(line, explicit)
			t.add(key, child)
		case child == nil:
			child = newTable(line, implicit)
			t.add(key, child)
		case child.kind != kTable:
			p.fail("%s is already defined as a value", strings.Join(path[:i+1], "."))
		case child.origin == dotted || child.origin == inline:
			p.fail("table %s is already defined", strings.Join(path[:i+1], "."))
		case last && child.origin == explicit:
			p.fail("table %s is defined twice", strings.Join(path, "."))
		case last:
			child.origin, child.line = explicit, line
		}
		t = child
	}
	p.cur = t
}

// keyValue reads `key = value` into t, creating the tables a dotted key names.
func (p *parser) keyValue(t *node) {
	line := p.line
	path := p.keyPath()
	p.skipSpaces()
	p.expect('=')
	p.skipSpaces()
	v := p.value(false)
	v.line = line
	p.place(t, path, v)
}

func (p *parser) place(t *node, path []string, v *node) {
	for i, key := range path[:len(path)-1] {
		child := t.entries[key]
		if child == nil {
			child = newTable(v.line, dotted)
			t.add(key, child)
		} else if child.kind != kTable || child.origin != dotted {
			p.failAt(v.line, "key %s is already defined", strings.Join(path[:i+1], "."))
		}
		t = child
	}
	last := path[len(path)-1]
	if t.entries[last] != nil {
		p.failAt(v.line, "key %s is defined twice", strings.Join(path, "."))
	}
	t.add(last, v)
}

func (p *parser) value(inInline bool) *node {
	switch p.peek() {
	case '"', '\'':
		return &node{kind: kString, str: p.str()}
	case '[':
		return p.array()
	case '{':
		if inInline {
			p.fail("inline tables cannot be nested")
		}
		return p.inlineTable()
	}
	return p.scalar()
}

// scalar reads a bare token, which must be an integer.
func (p *parser) scalar() *node {
	start := p.pos
	for !p.eof() && !strings.ContainsRune(" \t\r\n,]}#", rune(p.peek())) {
		p.pos++
	}
	tok := p.src[start:p.pos]
	if tok == "" {
		p.fail("missing value, found %s", p.describe())
	}
	if !integer.MatchString(tok) {
		p.fail("unsupported value %s: only strings, integers and arrays of strings are allowed", tok)
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		p.fail("integer %s is out of range", tok)
	}
	return &node{kind: kInt, num: n}
}

func (p *parser) str() string {
	quote := p.peek()
	if p.has(strings.Repeat(string(quote), 3)) {
		p.fail("multi-line strings are not supported")
	}
	p.pos++
	var b strings.Builder
	for {
		if p.eof() || p.peek() == '\n' || p.peek() == '\r' {
			p.fail("unterminated string")
		}
		c := p.peek()
		switch {
		case c == quote:
			p.pos++
			return b.String()
		case c == '\\' && quote == '"':
			p.escape(&b)
		case c < 0x20 && c != '\t' || c == 0x7f:
			p.fail("control character in string")
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
}

var simpleEscapes = map[byte]string{'b': "\b", 't': "\t", 'n': "\n", 'f': "\f", 'r': "\r", '"': `"`, '\\': `\`}

func (p *parser) escape(b *strings.Builder) {
	p.pos++
	c := p.peek()
	if s, ok := simpleEscapes[c]; ok {
		b.WriteString(s)
		p.pos++
		return
	}
	digits := map[byte]int{'u': 4, 'U': 8}[c]
	if digits == 0 {
		p.fail("invalid escape \\%s", p.describeEscape())
	}
	p.pos++
	if p.pos+digits > len(p.src) {
		p.fail("invalid \\%c escape", c)
	}
	code, err := strconv.ParseUint(p.src[p.pos:p.pos+digits], 16, 32)
	if err != nil || !utf8.ValidRune(rune(code)) {
		p.fail("invalid \\%c escape", c)
	}
	b.WriteRune(rune(code))
	p.pos += digits
}

func (p *parser) describeEscape() string {
	if p.eof() || p.peek() == '\n' {
		return "<end of line>"
	}
	return string(p.peek())
}

// array reads `[ "a", "b" ]`; it may span lines and carry comments.
func (p *parser) array() *node {
	p.pos++
	n := &node{kind: kArray}
	for {
		p.skipBlank()
		if p.peek() == ']' {
			p.pos++
			return n
		}
		if c := p.peek(); c != '"' && c != '\'' {
			p.fail("arrays may hold only strings, found %s", p.describe())
		}
		n.items = append(n.items, p.str())
		p.skipBlank()
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
		default:
			p.fail("expected , or ] in the array, found %s", p.describe())
		}
	}
}

// inlineTable reads `{ k = v, k = v }`, which must stay on one line.
func (p *parser) inlineTable() *node {
	line := p.line
	p.pos++
	n := newTable(line, inline)
	p.skipSpaces()
	if p.peek() == '}' {
		p.pos++
		return n
	}
	for {
		keyLine := p.line
		path := p.keyPath()
		if len(path) > 1 {
			p.fail("dotted keys are not supported in an inline table")
		}
		p.skipSpaces()
		p.expect('=')
		p.skipSpaces()
		v := p.value(true)
		v.line = keyLine
		p.place(n, path, v)
		p.skipSpaces()
		switch p.peek() {
		case ',':
			p.pos++
			p.skipSpaces()
			if p.peek() == '}' {
				p.fail("an inline table cannot end with a comma")
			}
		case '}':
			p.pos++
			if p.line != line {
				p.failAt(line, "an inline table must be on one line")
			}
			return n
		default:
			p.fail("expected , or } in the inline table, found %s", p.describe())
		}
	}
}
