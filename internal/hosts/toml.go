package hosts

import (
	"errors"
	"fmt"
	"slices"

	"github.com/BurntSushi/toml"
)

// Error is a violation in a host config file. A syntax error carries the
// 1-based line the TOML library reports; a rule violation has no line (0)
// and names its table and key in Msg instead.
type Error struct {
	Line int
	Msg  string
	pos  int // the violating key's place in the file, which orders violations
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

// kind is the type of a parsed value.
type kind int

const (
	kString kind = iota
	kInt
	kArray
	kTable
	kOther // any type host config never uses: floats, booleans, dates, other arrays
)

func (k kind) String() string {
	return [...]string{"a string", "an integer", "an array of strings", "a table", "another type"}[k]
}

// node is one parsed value, with its key's place in the file.
type node struct {
	pos     int
	kind    kind
	str     string
	num     int
	items   []string
	entries map[string]*node
	order   []string // the table's keys, in file order
}

// parse reads src as TOML and builds the tree the validator reads. Every key
// must be bare (letters, digits, - and _): host and harness names reach
// shells, paths and tmux.
func parse(src string) (*node, error) {
	var data map[string]any
	md, err := toml.Decode(src, &data)
	var pe toml.ParseError
	switch {
	case errors.As(err, &pe):
		return nil, &Error{Line: pe.Position.Line, Msg: pe.Message}
	case err != nil:
		return nil, &Error{Msg: err.Error()}
	}
	b := &builder{pos: map[string]int{}}
	for i, key := range md.Keys() {
		b.pos[key.String()] = i
	}
	root := b.table(nil, data)
	root.pos = -1
	if b.bad != nil {
		return nil, b.bad
	}
	return root, nil
}

// builder turns the library's decoded values into nodes.
type builder struct {
	pos map[string]int // each key's index in md.Keys()
	bad *Error         // the earliest key that is not bare
}

func (b *builder) value(key toml.Key, v any) *node {
	n := &node{pos: b.pos[key.String()], kind: kOther}
	switch v := v.(type) {
	case string:
		n.kind, n.str = kString, v
	case int64:
		n.kind, n.num = kInt, int(v)
	case []any:
		if items, ok := stringItems(v); ok {
			n.kind, n.items = kArray, items
		}
	case map[string]any:
		return b.table(key, v)
	}
	return n
}

// table builds a table node with its keys in file order. A table the file
// never names (the parent of a dotted key) takes its first key's place.
func (b *builder) table(key toml.Key, m map[string]any) *node {
	n := &node{kind: kTable, entries: map[string]*node{}}
	for name, v := range m {
		child := append(slices.Clip(key), name)
		if at := b.pos[child.String()]; !bareKey(name) && (b.bad == nil || at < b.bad.pos) {
			b.bad = &Error{Msg: fmt.Sprintf("key %s: use only letters, digits, - and _ in a key", child), pos: at}
		}
		n.entries[name] = b.value(child, v)
		n.order = append(n.order, name)
	}
	slices.SortFunc(n.order, func(x, y string) int { return n.entries[x].pos - n.entries[y].pos })
	n.pos = b.pos[key.String()]
	if _, named := b.pos[key.String()]; !named && len(n.order) > 0 {
		n.pos = n.entries[n.order[0]].pos
	}
	return n
}

// stringItems is the array's items when every one is a string.
func stringItems(items []any) ([]string, bool) {
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func bareKey(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c != '-' && c != '_' && (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}
