package hosts

import (
	"errors"
	"slices"
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
name = "basic \"quoted\" \t \u00e9"   # trailing comment
literal = 'C:\no\escapes'
count = 42
neg = -7
list = ["a", 'b', ]
none = []
tight = {a="1",b=2}

[outer.inner]
key = "v"
dotted.deep.leaf = 1
`)
	e := root.entries
	if e["name"].str != "basic \"quoted\" \t \u00e9" || e["literal"].str != `C:\no\escapes` {
		t.Errorf("strings: %q, %q", e["name"].str, e["literal"].str)
	}
	if e["count"].kind != kInt || e["count"].num != 42 || e["neg"].num != -7 {
		t.Errorf("integers: %+v, %+v", e["count"], e["neg"])
	}
	if e["list"].kind != kArray || !slices.Equal(e["list"].items, []string{"a", "b"}) || e["none"].kind != kArray || len(e["none"].items) != 0 {
		t.Errorf("arrays: %+v, %+v", e["list"], e["none"])
	}
	if tight := e["tight"]; tight.kind != kTable || tight.entries["a"].str != "1" || tight.entries["b"].num != 2 {
		t.Errorf("inline table: %+v", tight)
	}
	inner := e["outer"].entries["inner"]
	if inner.entries["key"].str != "v" || inner.entries["dotted"].entries["deep"].entries["leaf"].num != 1 {
		t.Errorf("outer.inner: %+v", inner)
	}
}

func TestParseKeepsFileOrder(t *testing.T) {
	root := mustParse(t, "z = 1\na = 2\n[m]\nq = 1\nb = 2\n[c.d]\nx = 1\n")
	if !slices.Equal(root.order, []string{"z", "a", "m", "c"}) || !slices.Equal(root.entries["m"].order, []string{"q", "b"}) {
		t.Errorf("order: %v, m: %v", root.order, root.entries["m"].order)
	}
	// A table the file never names (c, the parent of [c.d]) takes its first
	// key's place, so it still sorts after m.
	if c, m := root.entries["c"], root.entries["m"]; c.pos <= m.pos || c.pos != c.entries["d"].pos {
		t.Errorf("c at %d, c.d at %d, m at %d", c.pos, c.entries["d"].pos, m.pos)
	}
}

// The library accepts all of TOML. Every value host config never uses parses
// as kOther, which the validator rejects as the wrong type.
func TestParseOtherTypes(t *testing.T) {
	root := mustParse(t, "f = 1.5\nb = true\nd = 2026-10-21\nnums = [1]\nmixed = [\"x\", 2]\ntables = [{a = 1}]\n[[arr]]\nx = 1\n")
	for _, key := range []string{"f", "b", "d", "nums", "mixed", "tables", "arr"} {
		if got := root.entries[key].kind; got != kOther {
			t.Errorf("%s is %v, want another type", key, got)
		}
	}
}

// Wider TOML syntax yields only strings and integers, which escapes and
// plain integers already express.
func TestParseWiderSyntax(t *testing.T) {
	root := mustParse(t, "hex = 0x10\nbig = 1_000\nml = \"\"\"\nx\"\"\"\nlit = '''y'''\nt = {\n a = 1 }\n")
	e := root.entries
	if e["hex"].num != 16 || e["big"].num != 1000 || e["ml"].str != "x" || e["lit"].str != "y" || e["t"].entries["a"].num != 1 {
		t.Errorf("got %+v", e)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line int
		want string
	}{
		{"duplicate key", "a = 1\nb = 2\na = 3\n", 3, "Key 'a' has already been defined"},
		{"duplicate table", "[a]\n[b]\n[a]\n", 3, "Key 'a' has already been defined"},
		{"dotted key through value", "a = 1\na.b = 2\n", 2, "Key 'a' has already been defined"},
		{"invalid UTF-8", "a = 1\nb = \"\xff\"\n", 2, "invalid UTF-8"},
		{"missing value", "a =\n", 1, "expected value"},
		{"huge integer", "\nn = 99999999999999999999\n", 2, "out of range"},
		{"unterminated string", "a = 1\nb = \"x\n", 2, "strings cannot contain newlines"},
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

// Host and harness names reach shells, paths and tmux, so a quoted key that
// is not also a bare one is refused wherever it appears.
func TestParseRejectsKeysThatAreNotBare(t *testing.T) {
	for src, key := range map[string]string{
		"\"a b\" = 1\n":               `"a b"`,
		"[host.\"my.mac\"]\nx = 1\n":  `host."my.mac"`,
		"[host.'a/b']\n":              `host."a/b"`,
		"\"\" = 1\n":                  `""`,
		"bin = { \"x;y\" = \"p\" }\n": `bin."x;y"`,
		"ok = 1\n[t]\n\"a\" = 1\n\"b c\" = 2\n\"d e\" = 3\n": `t."b c"`,
	} {
		e := syntaxError(t, src)
		if e.Line != 0 || e.Msg != "key "+key+": use only letters, digits, - and _ in a key" {
			t.Errorf("%q: got %v, want key %s", src, e, key)
		}
	}
	// A quoted key spelled as a bare one is the same key.
	if root := mustParse(t, "[host.\"mac\"]\n'x' = 1\n"); root.entries["host"].entries["mac"].entries["x"].num != 1 {
		t.Error("quoted bare key")
	}
}

func TestErrorText(t *testing.T) {
	if got := (&Error{Line: 7, Msg: "boom"}).Error(); got != "line 7: boom" {
		t.Errorf("got %q", got)
	}
	if got := (&Error{Msg: "host.mac: boom"}).Error(); got != "host.mac: boom" {
		t.Errorf("got %q", got)
	}
}

func TestKindText(t *testing.T) {
	for k, want := range map[kind]string{kString: "a string", kInt: "an integer", kArray: "an array of strings", kTable: "a table", kOther: "another type"} {
		if k.String() != want {
			t.Errorf("kind %d = %q, want %q", k, k.String(), want)
		}
	}
}
