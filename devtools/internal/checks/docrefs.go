package checks

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	mdLink     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	codeSpan   = regexp.MustCompile("`([^`\n]+)`")
	lineCite   = regexp.MustCompile(`^[^\s]+\.\w+(?::\d+(?:-\d+)?|#L\d+(?:-L?\d+)?)$`)
	laneCite   = regexp.MustCompile(`\./dev test ([A-Za-z][\w-]*)`)
	headerLine = regexp.MustCompile(`^> \*\*Owns:\*\* .+ \*\*Not here:\*\* .+`)
	// Tracker references: issue or PR numbers, alone or after owner/repo, and
	// tracker URLs; plus plan file paths.
	trackerRefs = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[\s(\[,])#\d+\b`),
		regexp.MustCompile(`\b[\w.-]+/[\w.-]+#\d+\b`),
		regexp.MustCompile(`github\.com/[\w.-]+/[\w.-]+/(?:issues|pull|discussions)/\d+`),
		regexp.MustCompile(`(?:^|[^\w-])plans/[\w.-]+(?:/[\w.-]+)*\.\w+`),
	}
)

// proseLine is a line outside fenced code blocks.
type proseLine struct {
	number int
	text   string
}

func prose(text string) []proseLine {
	var lines []proseLine
	fenced := false
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			lines = append(lines, proseLine{number: i + 1, text: line})
		}
	}
	return lines
}

// citation is a relative link target, without its anchor, or a word of a
// code span that may name a path.
type citation struct {
	target string
	link   bool
}

// citations returns a line's relative link targets and the candidate path
// words of its code spans.
func citations(line string) []citation {
	var out []citation
	for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
		target := m[1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
			continue
		}
		if i := strings.Index(target, "#"); i >= 0 && !lineCite.MatchString(target) {
			target = target[:i]
		}
		out = append(out, citation{target: target, link: true})
	}
	for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
		for _, word := range strings.Fields(m[1]) {
			if !strings.ContainsAny(word, "<>*{}$|[]@~") && !strings.Contains(word, "://") &&
				!strings.Contains(word, "...") && !strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "/") {
				out = append(out, citation{target: strings.TrimPrefix(word, "./")})
			}
		}
	}
	return out
}

// pathLike reports whether a code-span word names a repository path: a bare
// Markdown file, or a slash path whose first segment exists or that ends in
// a file extension. Words like origin/main, and paths inside .git, are not.
func (d docsEnv) pathLike(doc, word string) bool {
	if !strings.Contains(word, "/") {
		return strings.HasSuffix(word, ".md")
	}
	first := strings.SplitN(word, "/", 2)[0]
	if first == ".git" {
		return false
	}
	return d.exists(path.Join(path.Dir(doc), first)) || d.exists(first) || path.Ext(strings.TrimSuffix(word, "/")) != ""
}

func (d docsEnv) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(d.Root, filepath.FromSlash(rel)))
	return err == nil
}

// resolves reports whether target, cited from doc, exists relative to the
// doc or the root, or is a gitignored output such as a gate result.
func (d docsEnv) resolves(doc, target string) bool {
	for _, candidate := range []string{path.Join(path.Dir(doc), target), path.Clean(target)} {
		if strings.HasPrefix(candidate, "../") {
			continue
		}
		if d.exists(candidate) {
			return true
		}
		// A directory rule (`out/`) matches only a path that ends in "/".
		for _, variant := range []string{candidate, candidate + "/"} {
			if _, err := d.Run(d.Root, "git", "check-ignore", "-q", "--no-index", variant); err == nil {
				return true
			}
		}
	}
	return false
}

// citationProblems checks that every link and cited path in doc exists, and
// that none points at a line.
func (d docsEnv) citationProblems(doc, text string) []string {
	var problems []string
	for _, line := range prose(text) {
		for _, c := range citations(line.text) {
			at := fmt.Sprintf("%s:%d", doc, line.number)
			switch {
			case lineCite.MatchString(c.target):
				problems = append(problems, at+": cite a file, never a line: "+c.target)
			case !c.link && !d.pathLike(doc, c.target):
				continue
			case !d.resolves(doc, c.target):
				problems = append(problems, at+": cited path does not exist: "+c.target)
			}
		}
	}
	return problems
}

// headerProblems checks for the one-line Owns / Not here header right after
// the title.
func headerProblems(doc, text string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 4 {
			break
		}
		if !strings.HasPrefix(line, "> **Owns:**") {
			continue
		}
		switch {
		case !headerLine.MatchString(line):
			return []string{doc + ": header must read `> **Owns:** … **Not here:** …`"}
		case i+1 < len(lines) && strings.HasPrefix(lines[i+1], ">"):
			return []string{doc + ": header must be one line"}
		case strings.Contains(line, "Load when"):
			return []string{doc + ": header carries no load trigger; the index in " + entryDoc + " does that"}
		}
		return nil
	}
	return []string{doc + ": no `> **Owns:** … **Not here:** …` header under the title"}
}

// laneProblems checks that every `./dev test <lane>` names a real lane.
func (d docsEnv) laneProblems(doc, text string) []string {
	var problems []string
	for i, line := range strings.Split(text, "\n") {
		for _, m := range laneCite.FindAllStringSubmatch(line, -1) {
			if !d.lanes[m[1]] {
				problems = append(problems, fmt.Sprintf("%s:%d: ./dev test %s names no lane in scripts/lanes.tsv", doc, i+1, m[1]))
			}
		}
	}
	return problems
}

// referenceProblems scans every persistent text file, docs and code, for
// tracker references and for paths into an ephemeral directory.
func (d docsEnv) referenceProblems() ([]string, error) {
	ephemeral := make([]*regexp.Regexp, 0, len(d.rules.ephemeral))
	for _, prefix := range d.rules.ephemeral {
		ephemeral = append(ephemeral, regexp.MustCompile(`(?:^|[^\w./-])`+regexp.QuoteMeta(prefix)+`[\w.-]`))
	}
	var problems []string
	for _, f := range d.files {
		if d.rules.isExempt(f) || f == "go.sum" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(d.Root, f))
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			at := fmt.Sprintf("%s:%d", f, i+1)
			for _, re := range trackerRefs {
				if m := re.FindString(line); m != "" {
					problems = append(problems, fmt.Sprintf("%s: tracker reference %q; docs and code never cite an issue, PR or plan", at, strings.TrimSpace(m)))
				}
			}
			for j, re := range ephemeral {
				if re.MatchString(line) {
					problems = append(problems, fmt.Sprintf("%s: cites a file in %s, which is ephemeral; cite the owning doc", at, d.rules.ephemeral[j]))
				}
			}
		}
	}
	return problems, nil
}
