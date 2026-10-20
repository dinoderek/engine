package checks

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// DocBudgets is the docs registry: budgets, sets and the directories the doc
// rules skip.
const DocBudgets = "scripts/doc-budgets.tsv"

const (
	entryDoc        = "AGENTS.md"
	entryLink       = "CLAUDE.md"
	alwaysLoadSet   = "always-load"
	alwaysLoadTitle = "## Always load"
	onDemandTitle   = "## Load on demand"
)

// docSet is a combined word budget over docs loaded together.
type docSet struct {
	limit   int
	members []string
}

// docRules is the parsed docs registry.
type docRules struct {
	defaultBudget int
	budgets       map[string]int
	sets          map[string]docSet
	exempt        []string // prefixes no doc rule applies to
	ephemeral     []string // exempt prefixes that persistent files never cite into
	unindexed     map[string]bool
}

func parseDocRules(text string) (docRules, error) {
	r := docRules{budgets: map[string]int{}, sets: map[string]docSet{}, unindexed: map[string]bool{}}
	for _, row := range repo.Rows(text) {
		f := row.Fields
		bad := fmt.Errorf("%s:%d: malformed row", DocBudgets, row.Line)
		switch {
		case f[0] == "budget" && len(f) == 3:
			n, err := strconv.Atoi(f[2])
			if err != nil {
				return r, bad
			}
			if f[1] == "*" {
				r.defaultBudget = n
			} else {
				r.budgets[f[1]] = n
			}
		case f[0] == "set" && len(f) == 4:
			n, err := strconv.Atoi(f[2])
			if err != nil {
				return r, bad
			}
			r.sets[f[1]] = docSet{limit: n, members: strings.Split(f[3], ",")}
		case f[0] == "exempt" && len(f) == 2:
			r.exempt = append(r.exempt, f[1])
		case f[0] == "ephemeral" && len(f) == 2:
			r.exempt = append(r.exempt, f[1])
			r.ephemeral = append(r.ephemeral, f[1])
		case f[0] == "unindexed" && len(f) == 2:
			r.unindexed[f[1]] = true
		default:
			return r, bad
		}
	}
	if r.defaultBudget == 0 {
		return r, fmt.Errorf("%s: no default budget row (budget\t*\t<words>)", DocBudgets)
	}
	return r, nil
}

func (r docRules) isExempt(file string) bool {
	for _, p := range r.exempt {
		if strings.HasPrefix(file, p) {
			return true
		}
	}
	return false
}

func (r docRules) budget(doc string) int {
	if n, ok := r.budgets[doc]; ok {
		return n
	}
	return r.defaultBudget
}

// docsEnv is what the doc rules read: the persistent docs and every
// repository file, so cited paths are judged against the same tree in CI.
type docsEnv struct {
	Env
	rules docRules
	files []string
	docs  map[string]string // persistent doc -> text
	lanes map[string]bool
}

// Docs enforces the doc rules: word budgets, links and cited paths, the
// Owns/Not here header, index completeness, lane citations, and no tracker
// or ephemeral references in docs or code.
func Docs(e Env) int {
	const lane = "docs-check"
	d, err := loadDocs(e)
	if err != nil {
		return e.fail(lane, err)
	}
	problems := d.budgetProblems()
	problems = append(problems, d.indexProblems()...)
	problems = append(problems, d.entryLinkProblems()...)
	for _, doc := range sortedDocs(d.docs) {
		problems = append(problems, headerProblems(doc, d.docs[doc])...)
		problems = append(problems, d.citationProblems(doc, d.docs[doc])...)
		problems = append(problems, d.laneProblems(doc, d.docs[doc])...)
	}
	refs, err := d.referenceProblems()
	if err != nil {
		return e.fail(lane, err)
	}
	problems = append(problems, refs...)
	return e.report(lane, problems, fmt.Sprintf("%d docs follow the doc rules", len(d.docs)))
}

func loadDocs(e Env) (docsEnv, error) {
	d := docsEnv{Env: e, docs: map[string]string{}, lanes: map[string]bool{"for": true}}
	text, err := os.ReadFile(filepath.Join(e.Root, DocBudgets))
	if err != nil {
		return d, err
	}
	if d.rules, err = parseDocRules(string(text)); err != nil {
		return d, err
	}
	if d.files, err = repo.Files(e.Run, e.Root); err != nil {
		return d, err
	}
	for _, f := range d.files {
		if strings.HasSuffix(f, ".md") && !d.rules.isExempt(f) {
			body, err := os.ReadFile(filepath.Join(e.Root, f))
			if err != nil {
				return d, err
			}
			d.docs[f] = string(body)
		}
	}
	registry, err := os.ReadFile(filepath.Join(e.Root, "scripts/lanes.tsv"))
	if err != nil {
		return d, err
	}
	for _, row := range repo.Rows(string(registry)) {
		d.lanes[row.Fields[0]] = true
	}
	return d, nil
}

func sortedDocs(docs map[string]string) []string {
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func inSet(doc string, members []string) bool {
	for _, m := range members {
		if doc == m || (strings.HasSuffix(m, "/") && strings.HasPrefix(doc, m)) {
			return true
		}
	}
	return false
}

// budgetProblems checks each doc's budget and every set's combined budget,
// and prints the counts so a passing run shows the headroom.
func (d docsEnv) budgetProblems() []string {
	var problems []string
	for _, doc := range sortedDocs(d.docs) {
		words, limit := len(strings.Fields(d.docs[doc])), d.rules.budget(doc)
		fmt.Fprintf(d.Out, "docs-check: %5d / %5d words  %s\n", words, limit, doc)
		if words > limit {
			problems = append(problems, fmt.Sprintf("%s: %d words, over its %d-word budget; split or trim it", doc, words, limit))
		}
	}
	for doc := range d.rules.budgets {
		if _, ok := d.docs[doc]; !ok {
			problems = append(problems, fmt.Sprintf("%s: budget row for %s, which is not a persistent doc", DocBudgets, doc))
		}
	}
	for _, name := range sortedSetNames(d.rules.sets) {
		set := d.rules.sets[name]
		total := 0
		for doc, text := range d.docs {
			if inSet(doc, set.members) {
				total += len(strings.Fields(text))
			}
		}
		fmt.Fprintf(d.Out, "docs-check: %5d / %5d words  set %s\n", total, set.limit, name)
		if total > set.limit {
			problems = append(problems, fmt.Sprintf("set %s: %d words, over its %d-word budget", name, total, set.limit))
		}
		for _, m := range set.members {
			if !anyDoc(d.docs, m) {
				problems = append(problems, fmt.Sprintf("set %s: member %s is not a persistent doc", name, m))
			}
		}
	}
	return problems
}

func sortedSetNames(sets map[string]docSet) []string {
	names := make([]string, 0, len(sets))
	for n := range sets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func anyDoc(docs map[string]string, member string) bool {
	for doc := range docs {
		if inSet(doc, []string{member}) {
			return true
		}
	}
	return false
}

// section returns the lines of text under heading, up to the next "## ".
func section(text, heading string) string {
	_, rest, ok := strings.Cut(text, "\n"+heading+"\n")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// sectionDocs lists the persistent docs a section of the entry doc cites.
func (d docsEnv) sectionDocs(heading string) map[string]bool {
	cited := map[string]bool{}
	for _, line := range prose(section(d.docs[entryDoc], heading)) {
		for _, c := range citations(line.text) {
			if doc := path.Clean(c.target); strings.HasSuffix(doc, ".md") {
				cited[doc] = true
			}
		}
	}
	return cited
}

// indexProblems checks that the entry doc routes to every persistent doc, and
// that its always-load list matches the always-load set.
func (d docsEnv) indexProblems() []string {
	if _, ok := d.docs[entryDoc]; !ok {
		return []string{entryDoc + " is missing: it is the single agent entrypoint"}
	}
	always := d.sectionDocs(alwaysLoadTitle)
	onDemand := d.sectionDocs(onDemandTitle)
	var problems []string
	for _, doc := range sortedDocs(d.docs) {
		if !d.rules.unindexed[doc] && !always[doc] && !onDemand[doc] {
			problems = append(problems, fmt.Sprintf("%s is in neither %q nor %q in %s: add a load trigger", doc, alwaysLoadTitle, onDemandTitle, entryDoc))
		}
	}
	set, ok := d.rules.sets[alwaysLoadSet]
	if !ok {
		return append(problems, fmt.Sprintf("%s: no %q set", DocBudgets, alwaysLoadSet))
	}
	always[entryDoc] = true
	for doc := range always {
		if !inSet(doc, set.members) {
			problems = append(problems, fmt.Sprintf("%s is listed under %q but is not in the %s set", doc, alwaysLoadTitle, alwaysLoadSet))
		}
	}
	for _, m := range set.members {
		if !always[m] {
			problems = append(problems, fmt.Sprintf("%s is in the %s set but not listed under %q", m, alwaysLoadSet, alwaysLoadTitle))
		}
	}
	sort.Strings(problems)
	return problems
}

// entryLinkProblems checks that CLAUDE.md is a symlink to the entry doc.
func (d docsEnv) entryLinkProblems() []string {
	target, err := os.Readlink(filepath.Join(d.Root, entryLink))
	if err != nil || target != entryDoc {
		return []string{entryLink + " must be a symlink to " + entryDoc}
	}
	return nil
}
