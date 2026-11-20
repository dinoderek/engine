package reconcile

import "testing"

func TestADeadSessionsEndActionCarriesItsPaneTail(t *testing.T) {
	s := gone()
	s.PaneTail = "boom"
	acts := Decide(view(plan("planned"), with(task(2, "running"), s)))
	if len(acts) != 1 || acts[0].Kind != EndAttempt || acts[0].Tail != "boom" {
		t.Fatalf("actions %+v", acts)
	}
}
