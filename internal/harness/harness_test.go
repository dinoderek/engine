package harness_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
)

func TestAdaptersFindByNameWithTheFirstAsDefault(t *testing.T) {
	a, b := &harnesstest.Fake{Called: "alpha"}, &harnesstest.Fake{Called: "beta"}
	set := harness.NewAdapters(a, b)
	if got := set.Names(); strings.Join(got, ",") != "alpha,beta" {
		t.Errorf("names %v", got)
	}
	for name, want := range map[string]string{"": "alpha", "alpha": "alpha", "beta": "beta"} {
		if got, ok := set.Get(name); !ok || got.Name() != want {
			t.Errorf("Get(%q) = %v, %v; want %s", name, got, ok, want)
		}
	}
	if _, ok := set.Get("gamma"); ok {
		t.Error("an unknown harness was found")
	}
	if _, ok := harness.NewAdapters().Get(""); ok {
		t.Error("no adapters has a default")
	}
}

func TestLookupNamesTheHarnessesThereAre(t *testing.T) {
	set := harness.NewAdapters(&harnesstest.Fake{Called: "alpha"}, &harnesstest.Fake{Called: "beta"})
	if a, err := harness.Lookup(set, "beta"); err != nil || a.Name() != "beta" {
		t.Errorf("beta: %v, %v", a, err)
	}
	if _, err := harness.Lookup(set, "gamma"); err == nil || err.Error() != `unknown harness "gamma": want alpha or beta` {
		t.Errorf("gamma: %v", err)
	}
	if _, err := harness.Lookup(nil, ""); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Errorf("no adapters: %v", err)
	}
}

func TestEnvReadsNothingWithoutItsParts(t *testing.T) {
	var e harness.Env
	if e.Var("HOME") != "" || time.Since(e.Time()) > time.Minute {
		t.Errorf("empty env: %q, %v", e.Var("HOME"), e.Time())
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e = harness.Env{Getenv: func(k string) string { return k + "!" }, Now: func() time.Time { return at }}
	if e.Var("X") != "X!" || !e.Time().Equal(at) {
		t.Errorf("env: %q, %v", e.Var("X"), e.Time())
	}
}
