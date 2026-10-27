package run

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/tracker"
)

// harnesses orders unpinned choices by live load divided by weight. Rational
// arithmetic keeps ties exact even for large integer weights.
func (f Fleet) harnesses(c tracker.Card, running map[string]int) ([]string, error) {
	pinned, _, err := tracker.ParseHarnessTier(c.Body)
	if err != nil {
		return nil, err
	}
	if pinned != "" {
		return []string{pinned}, nil
	}
	if len(f.HarnessRatio) == 0 {
		return []string{f.DefaultHarness}, nil
	}
	var names []string
	for name, weight := range f.HarnessRatio {
		if weight > 0 {
			names = append(names, name)
		}
	}
	score := func(name string) *big.Rat {
		return new(big.Rat).SetFrac(big.NewInt(int64(running[name])+1), big.NewInt(int64(f.HarnessRatio[name])))
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(score(a).Cmp(score(b)), cmp.Compare(preference(a, f.DefaultHarness), preference(b, f.DefaultHarness)), strings.Compare(a, b))
	})
	return names, nil
}

// choose tries every eligible harness before holding a card back. Notify is
// true only when no choice could fit even with every slot free.
func (f Fleet) choose(c tracker.Card, free map[string]slots, running map[string]int) (string, HostLoad, string, bool) {
	names, err := f.harnesses(c, running)
	if err != nil {
		return "", HostLoad{}, "no host can take it: " + err.Error(), true
	}
	var reasons []string
	possible := false
	for _, name := range names {
		fits, why := f.fits(c, name)
		if why != "" {
			reasons = append(reasons, "no host can take it: "+why)
			continue
		}
		possible = true
		if limit := f.Caps[name]; running[name] >= limit {
			reasons = append(reasons, fmt.Sprintf("%s is at its cap of %d sessions", name, limit))
			continue
		}
		if best, ok := pick(fits, free, f.heavy(c)); ok {
			return name, best, "", false
		}
		reasons = append(reasons, "no free "+slotKind(f.heavy(c))+" on "+slotList(fits, free, f.heavy(c)))
	}
	return "", HostLoad{}, strings.Join(reasons, "; "), !possible
}

func preference(name, fallback string) int {
	if name == fallback {
		return 0
	}
	return 1
}
