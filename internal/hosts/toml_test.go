package hosts

import (
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *node {
	t.Helper()
	root, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	return root
}

func syntaxError(t *testing.T, src string) *Error {
	t.Helper()
	_, err := parse(src)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("parse(%q) = %v, want an *Error", src, err)
	}
	return e
}

func TestParseValues(t *testing.T) {
	root := mustParse(t, `# a comment
name = "basic \"quoted\" \t \u00e9 \U0001F600"   # trailing comment
literal = 'C:\no\escapes'
count = 42
neg = -7
plus = +3
zero = 0
empty = ""
list = ["a", 'b', ]
spread = [
  "x",  # first
  "y",
]
none = []
tight = {a="1",b=2}
bare = {}

[outer.inner]
key = "v"
dotted.deep.leaf = 1
`)
	e := root.entries
	checks := []struct {
		got, want string
	}{
		{e["name"].str, "basic \"quoted\" \t \u00e9 \U0001F600"},
		{e["literal"].str, `C:\no\escapes`},
		{e["empty"].str, ""},
		{strings.Join(e["list"].items, ","), "a,b"},
		{strings.Join(e["spread"].items, ","), "x,y"},
		{strings.Join(e["none"].items, ","), ""},
		{e["tight"].entries["a"].str, "1"},
		{e["outer"].entries["inner"].entries["key"].str, "v"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	for key, want := range map[string]int{"count": 42, "neg": -7, "plus": 3, "zero": 0} {
		if e[key].kind != kInt || e[key].num != want {
			t.Errorf("%s = %+v, want integer %d", key, e[key], want)
		}
	}
	if got := e["tight"].entries["b"]; got.kind != kInt || got.num != 2 {
		t.Errorf("tight.b = %+v", got)
	}
	if len(e["bare"].entries) != 0 || e["bare"].kind != kTable {
		t.Errorf("bare = %+v", e["bare"])
	}
	inner := e["outer"].entries["inner"]
	if leaf := inner.entries["dotted"].entries["deep"].entries["leaf"]; leaf.num != 1 || leaf.line != 20 {
		t.Errorf("dotted leaf = %+v, want 1 on line 20", leaf)
	}
}

func TestParseLines(t *testing.T) {
	root := mustParse(t, "a = 1\n\n[t]\r\nb = [\n\"x\",\n]\n# c\nc = 2\n")
	if got := root.entries["t"].line; got != 3 {
		t.Errorf("table t on line %d, want 3", got)
	}
	if got := root.entries["t"].entries["b"].line; got != 4 {
		t.Errorf("t.b on line %d, want 4", got)
	}
	if got := root.entries["t"].entries["c"].line; got != 8 {
		t.Errorf("t.c on line %d, want 8", got)
	}
}

func TestParseTableOrder(t *testing.T) {
	// A parent made by a child's header can still be defined once; so can a
	// table opened twice through different children.
	root := mustParse(t, "[a.b]\nx = 1\n[a]\ny = 2\n[a.c]\nz = 3\n")
	a := root.entries["a"]
	if a.origin != explicit || len(a.entries) != 3 || a.line != 3 {
		t.Errorf("a = %+v", a)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line int
		want string
	}{
		{"multi-line basic string", "a = 1\nb = \"\"\"x\"\"\"\n", 2, "multi-line strings are not supported"},
		{"multi-line literal string", "b = '''x'''\n", 1, "multi-line strings are not supported"},
		{"array of tables", "[a]\n[[b]]\n", 2, "arrays of tables are not supported"},
		{"date", "d = 2026-10-21\n", 1, "unsupported value 2026-10-21"},
		{"time", "d = 07:32:00\n", 1, "unsupported value 07:32:00"},
		{"float", "f = 1.5\n", 1, "unsupported value 1.5"},
		{"boolean", "\nb = true\n", 2, "unsupported value true"},
		{"hex", "n = 0x10\n", 1, "unsupported value 0x10"},
		{"underscore", "n = 1_000\n", 1, "unsupported value 1_000"},
		{"leading zero", "n = 01\n", 1, "unsupported value 01"},
		{"huge integer", "n = 99999999999999999999\n", 1, "out of range"},
		{"integer array", "a = [1]\n", 1, "arrays may hold only strings"},
		{"mixed array", "a = [\"x\",\n  2]\n", 2, "arrays may hold only strings"},
		{"nested array", "a = [[\"x\"]]\n", 1, "arrays may hold only strings"},
		{"unclosed array", "a = [\"x\"\n", 2, "expected , or ]"},
		{"missing array comma", "a = [\"x\" \"y\"]\n", 1, "expected , or ]"},
		{"quoted key", "\"a\" = 1\n", 1, "quoted keys are not supported"},
		{"literal key", "[a.'b']\n", 1, "quoted keys are not supported"},
		{"nested inline table", "a = { b = { c = 1 } }\n", 1, "cannot be nested"},
		{"dotted key in inline table", "a = { b.c = 1 }\n", 1, "dotted keys are not supported in an inline table"},
		{"multi-line inline table", "a = {\n b = 1 }\n", 1, "expected a key"},
		{"inline table spanning an array", "a = { b = [\n\"x\"] }\n", 1, "must be on one line"},
		{"inline trailing comma", "a = { b = 1, }\n", 1, "cannot end with a comma"},
		{"inline missing comma", "a = { b = 1 c = 2 }\n", 1, "expected , or }"},
		{"inline duplicate key", "a = { b = 1, b = 2 }\n", 1, "b is defined twice"},
		{"inline unterminated", "a = { b = 1\n", 1, "expected , or }"},
		{"unterminated string", "a = \"x\n", 1, "unterminated string"},
		{"unterminated literal at end", "a = 'x", 1, "unterminated string"},
		{"bad escape", "a = \"\\q\"\n", 1, `invalid escape \q`},
		{"escape at end of line", "a = \"\\\n", 1, "invalid escape"},
		{"short unicode escape", "a = \"\\u12\"\n", 1, `invalid \u escape`},
		{"surrogate escape", "a = \"\\ud800\"\n", 1, `invalid \u escape`},
		{"truncated escape", "a = \"\\U0001", 1, `invalid \U escape`},
		{"control character", "a = \"x\x01\"\n", 1, "control character"},
		{"missing value", "a =\n", 1, "missing value"},
		{"missing equals", "a 1\n", 1, `expected '=', found '1'`},
		{"missing key", "= 1\n", 1, "expected a key"},
		{"junk after value", "a = 1 b\n", 1, "after the value"},
		{"junk after header", "[a] b\n", 1, "after the value"},
		{"unclosed header", "[a\n", 1, `expected ']'`},
		{"empty header", "[]\n", 1, "expected a key"},
		{"duplicate key", "a = 1\nb = 2\na = 3\n", 3, "key a is defined twice"},
		{"duplicate table", "[a]\n[b]\n[a]\n", 3, "table a is defined twice"},
		{"table over value", "a = 1\n[a]\n", 2, "a is already defined as a value"},
		{"header through value", "a = 1\n[a.b]\n", 2, "a is already defined as a value"},
		{"dotted key through value", "a = 1\na.b = 2\n", 2, "key a is already defined"},
		{"header reopens dotted table", "[a]\nb.c = 1\n[a.b]\n", 3, "table a.b is already defined"},
		{"header reopens inline table", "a = { b = 1 }\n[a]\n", 2, "table a is already defined"},
		{"header extends empty inline table", "a = {}\n[a.b]\n", 2, "table a is already defined"},
		{"dotted key reopens header table", "[a.b]\n[a]\nb.c = 1\n", 3, "key b is already defined"},
		{"invalid UTF-8", "a = \"\xff\"\n", 1, "not valid UTF-8"},
		{"stray bracket", "a = ]\n", 1, "missing value, found ']'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := syntaxError(t, c.src)
			if e.Line != c.line || !strings.Contains(e.Msg, c.want) {
				t.Errorf("got line %d: %s\nwant line %d containing %q", e.Line, e.Msg, c.line, c.want)
			}
		})
	}
}

func TestErrorText(t *testing.T) {
	if got := (&Error{Line: 7, Msg: "boom"}).Error(); got != "line 7: boom" {
		t.Errorf("got %q", got)
	}
}

func TestKindText(t *testing.T) {
	for k, want := range map[kind]string{kString: "a string", kInt: "an integer", kArray: "an array of strings", kTable: "a table"} {
		if k.String() != want {
			t.Errorf("kind %d = %q, want %q", k, k.String(), want)
		}
	}
}
