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
	// Link targets: inline links (optionally <bracketed>, optionally titled),
	// reference definitions and HTML hrefs.
	linkForms = []*regexp.Regexp{
		regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`),
		regexp.MustCompile(`^\s*\[[^\]]+\]:\s*<?([^\s>]+)>?`),
		regexp.MustCompile(`href="([^"]+)"`),
	}
	codeSpan   = regexp.MustCompile("`([^`\n]+)`")
	lineSuffix = regexp.MustCompile(`(?::\d+(?:-\d+)?|#L\d+(?:-L?\d+)?)$`)
	laneCite   = regexp.MustCompile(`\./dev test ([A-Za-z][\w-]*)`)
	headerLine = regexp.MustCompile(`^> \*\*Owns:\*\* .+ \*\*Not here:\*\* .+`)
	// Tracker references: issue or PR numbers, alone, after owner/repo or
	// after a word such as "issue" or "PR"; issue and PR URL paths; plan file
	// paths. A pattern's first group, if any, is the reference itself.
	trackerRefs = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[^\w&/])(#\d+)(?:$|[^\w-])`),
		regexp.MustCompile(`\b[\w.-]+/[\w.-]+#\d+\b`),
		regexp.MustCompile(`(?i)\b(?:issues?|prs?|pull requests?|gh)[\s:#-]+\d+\b`),
		regexp.MustCompile(`(?:^|/)(?:issues|pulls?|discussions)/\d+`),
		regexp.MustCompile(`(?:^|[^\w-])plans/[\w.-]+(?:/[\w.-]+)*\.\w+`),
	}
	// fileExt is the extensions that make a code-span word a file path.
	fileExt = map[string]bool{
		".md": true, ".go": true, ".mod": true, ".sum": true, ".tsv": true, ".yml": true,
		".yaml": true, ".json": true, ".toml": true, ".sh": true, ".txt": true,
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
	for _, form := range linkForms {
		for _, m := range form.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
				continue
			}
			if i := strings.Index(target, "#"); i >= 0 && !lineSuffix.MatchString(target) {
				target = target[:i]
			}
			out = append(out, citation{target: target, link: true})
		}
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
// a file extension. Words like origin/main and gopkg.in/yaml.v3, and paths
// inside .git, are not.
func (d docsEnv) pathLike(doc, word string) bool {
	if !strings.Contains(word, "/") {
		return strings.HasSuffix(word, ".md")
	}
	first := strings.SplitN(word, "/", 2)[0]
	if first == ".git" {
		return false
	}
	return d.exists(path.Join(path.Dir(doc), first)) || d.exists(first) || fileExt[path.Ext(strings.TrimSuffix(word, "/"))]
}

// lineCitation reports whether target points at a line of a file, such as
// main.go:12 or a.md#L3. Addresses such as 127.0.0.1:8080 are not.
func (d docsEnv) lineCitation(doc, target string) bool {
	file := lineSuffix.ReplaceAllString(target, "")
	return file != target && (fileExt[path.Ext(file)] || d.pathLike(doc, file))
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
			case d.lineCitation(doc, c.target):
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

// laneProblems checks that every `./dev test <lane>` names a real lane,
// fenced examples included: they are commands an agent copies.
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

// referenceProblems scans every file outside the ephemeral directories, docs
// and code alike (exempt ones included), for tracker references and for paths
// into an ephemeral directory, however they are spelled relative.
func (d docsEnv) referenceProblems() ([]string, error) {
	ephemeral := make([]*regexp.Regexp, 0, len(d.rules.ephemeral))
	for _, prefix := range d.rules.ephemeral {
		ephemeral = append(ephemeral, regexp.MustCompile(`(?:^|[^\w./-])(?:\.\.?/)*`+regexp.QuoteMeta(prefix)+`[\w.-]`))
	}
	var problems []string
	for _, f := range d.files {
		if d.rules.isEphemeral(f) || f == "go.sum" {
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
				if m := re.FindStringSubmatch(line); m != nil {
					ref := m[len(m)-1]
					problems = append(problems, fmt.Sprintf("%s: tracker reference %q; docs and code never cite an issue, PR or plan", at, strings.TrimSpace(ref)))
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
