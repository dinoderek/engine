package baseline

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse("b.tsv", "# header\na.go\tfunlen\t2\n", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Counts{"a.go\tfunlen": 2}); !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %v, want %v", got, want)
	}
	for _, bad := range []string{"a.go\t2\n", "a.go\tfunlen\tx\n", "a.go\tfunlen\t0\n"} {
		if _, err := Parse("b.tsv", bad, 2); err == nil {
			t.Errorf("Parse(%q): want an error", bad)
		}
	}
}

func TestCompare(t *testing.T) {
	base := Counts{"a.go": 2, "b.go": 1, "c.go": 1}
	found := Counts{"a.go": 3, "b.go": 1, "d.go": 1}
	want := []string{
		"a.go: 3 found, baseline x allows 2",
		"d.go: 1 found, baseline x allows 0",
		"c.go: baseline x allows 1 but 0 remain; lower the row (delete it at 0)",
	}
	if got := Compare("x", base, found); !reflect.DeepEqual(got, want) {
		t.Errorf("Compare =\n%q\nwant\n%q", got, want)
	}
	if got := Compare("x", Counts{}, Counts{}); got != nil {
		t.Errorf("empty Compare = %v", got)
	}
}
