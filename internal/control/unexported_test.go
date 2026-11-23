package control

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The records live in state, which callers outside this package reach only
// through a Tx. This fails when an exported function, method, field or
// variable names state in its type, which would hand a caller the records.
func TestNoExportedDeclarationExposesTheRecordState(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed++
		for _, decl := range file.Decls {
			for _, exposed := range exposures(decl) {
				t.Errorf("%s: %s mentions state in an exported declaration", name, exposed)
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no source files parsed")
	}
}

// exposures are the exported declarations in decl whose type mentions state.
func exposures(decl ast.Decl) []string {
	var out []string
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Name.IsExported() && (d.Recv == nil || exportedReceiver(d.Recv)) && mentionsState(d.Type) {
			out = append(out, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			out = append(out, specExposures(spec)...)
		}
	}
	return out
}

func specExposures(spec ast.Spec) []string {
	var out []string
	switch s := spec.(type) {
	case *ast.ValueSpec:
		for _, n := range s.Names {
			if n.IsExported() && s.Type != nil && mentionsState(s.Type) {
				out = append(out, n.Name)
			}
		}
	case *ast.TypeSpec:
		if st, ok := s.Type.(*ast.StructType); ok && s.Name.IsExported() {
			for _, f := range st.Fields.List {
				for _, n := range f.Names {
					if n.IsExported() && mentionsState(f.Type) {
						out = append(out, s.Name.Name+"."+n.Name)
					}
				}
			}
		}
	}
	return out
}

func exportedReceiver(recv *ast.FieldList) bool {
	for _, f := range recv.List {
		t := f.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if id, ok := t.(*ast.Ident); ok && id.IsExported() {
			return true
		}
	}
	return false
}

func mentionsState(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			// A parameter or field may be named state; only its type counts.
			found = found || mentionsState(x.Type)
			return false
		case *ast.Ident:
			found = found || x.Name == "state"
		}
		return !found
	})
	return found
}

func TestTheExposureCheckFindsAnExportedSignatureWithState(t *testing.T) {
	src := "package p\nfunc Leak() state { return state{} }\nfunc (s *Store) Peek(fn func(*state)) {}\nfunc quiet() state { return state{} }\nfunc Safe(fn func(*Tx)) {}\ntype T struct{ Leaky state; hidden state; Shown int }\nvar V state\n"
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, decl := range f.Decls {
		got = append(got, exposures(decl)...)
	}
	if want := "Leak Peek T.Leaky V"; strings.Join(got, " ") != want {
		t.Errorf("exposures %v, want %s", got, want)
	}
}
