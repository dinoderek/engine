package hosts

import (
	"fmt"
	"slices"
)

// ValidateRatio refuses unknown harnesses, negative weights and a ratio with
// no positive weight. A nil ratio keeps the default harness.
func (c *Config) ValidateRatio(weights map[string]int) error {
	if weights == nil {
		return nil
	}
	names := make([]string, 0, len(weights))
	for name := range weights {
		names = append(names, name)
	}
	slices.Sort(names)
	positive := false
	for _, name := range names {
		if _, ok := c.Harnesses[name]; !ok {
			return fmt.Errorf("harness_ratio: harness %q is not defined under [harness]", name)
		}
		if weights[name] < 0 {
			return fmt.Errorf("harness_ratio: weight for %q must be nonnegative", name)
		}
		positive = positive || weights[name] > 0
	}
	if !positive {
		return fmt.Errorf("harness_ratio must have a positive weight")
	}
	return nil
}
