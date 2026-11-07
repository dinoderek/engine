package controlrun

import (
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestStructuredExecutionFieldsOverrideBodySections(t *testing.T) {
	item := control.Item{ID: "t1", Harness: "claude", Tier: "light", Requires: []string{"linux"}, Body: "## Outcome\nWorks\n## Harness / tier\ncodex deep\n## Requires\nios\n## Tests\nProve it"}
	body := executionBody(item)
	harness, tier, err := tracker.ParseHarnessTier(body)
	if err != nil || harness != "claude" || tier != "light" {
		t.Fatal(body, harness, tier, err)
	}
	req := tracker.ParseRequires(body)
	if len(req) != 1 || req[0] != "linux" {
		t.Fatal(req)
	}
	item.Harness = ""
	item.Tier = ""
	harness, tier, err = tracker.ParseHarnessTier(executionBody(item))
	if err != nil || harness != "" || tier != "standard" {
		t.Fatal("body overrode default structured fields")
	}
}
