package hosts

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ParseRatio parses comma-separated harness=weight pairs. Names are checked
// against the config by ValidateRatio.
func ParseRatio(src string) (map[string]int, error) {
	weights := map[string]int{}
	for _, pair := range strings.Split(src, ",") {
		name, value, ok := strings.Cut(pair, "=")
		n, err := strconv.Atoi(value)
		if !ok || name == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("invalid harness ratio %q: want harness=nonnegative-integer", pair)
		}
		if _, exists := weights[name]; exists {
			return nil, fmt.Errorf("harness ratio repeats %q", name)
		}
		weights[name] = n
	}
	return weights, nil
}

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
