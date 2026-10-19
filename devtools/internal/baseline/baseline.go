// Package baseline holds the shrink-only baselines: counts of violations that
// existed when a gate landed. A lane tolerates up to the recorded count for a
// key. Above it the lane fails; below it the lane also fails until the count
// is lowered, so a baseline can only shrink.
package baseline

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// Counts maps a violation key (its fields joined by tabs) to a count.
type Counts map[string]int

// Parse reads a baseline file: rows of keyFields tab-separated fields followed
// by a count.
func Parse(name, text string, keyFields int) (Counts, error) {
	counts := Counts{}
	for _, row := range repo.Rows(text) {
		if len(row.Fields) != keyFields+1 {
			return nil, fmt.Errorf("%s:%d: want %d tab-separated fields", name, row.Line, keyFields+1)
		}
		n, err := strconv.Atoi(row.Fields[keyFields])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s:%d: count must be a positive integer", name, row.Line)
		}
		counts[strings.Join(row.Fields[:keyFields], "\t")] = n
	}
	return counts, nil
}

// Compare returns one problem per key whose found count exceeds the baseline,
// and one per baseline row that is now too high.
func Compare(name string, base, found Counts) []string {
	var problems []string
	for _, key := range sortedKeys(found) {
		if found[key] > base[key] {
			problems = append(problems, fmt.Sprintf("%s: %d found, baseline %s allows %d",
				show(key), found[key], name, base[key]))
		}
	}
	for _, key := range sortedKeys(base) {
		if found[key] < base[key] {
			problems = append(problems, fmt.Sprintf(
				"%s: baseline %s allows %d but %d remain; lower the row (delete it at 0)",
				show(key), name, base[key], found[key]))
		}
	}
	return problems
}

func show(key string) string { return strings.ReplaceAll(key, "\t", " ") }

func sortedKeys(c Counts) []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
