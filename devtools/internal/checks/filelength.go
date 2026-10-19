package checks

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/baseline"
	"github.com/dinoderek/engine/devtools/internal/repo"
)

// FileLengthBaseline holds the tolerated line counts of files over the limit.
const FileLengthBaseline = "scripts/file-length-baseline.tsv"

// FileLength fails when a text file has more than -max lines. -exclude takes
// comma-separated patterns, matched against the file name, or the whole path
// when the pattern holds a "/".
func FileLength(e Env, args []string) int {
	const lane = "file-length"
	var maximum int
	var exclude string
	if err := e.flags(lane, args, func(fs *flag.FlagSet) {
		fs.IntVar(&maximum, "max", 0, "maximum lines per file")
		fs.StringVar(&exclude, "exclude", "", "comma-separated patterns to skip")
	}); err != nil {
		return 2
	}
	files, err := repo.Files(e.Run, e.Root)
	if err != nil {
		return e.fail(lane, err)
	}
	found := baseline.Counts{}
	checked := 0
	for _, f := range files {
		if excluded(f, exclude) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.Root, f))
		if err != nil {
			return e.fail(lane, err)
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			continue // binary
		}
		checked++
		if n := lineCount(data); n > maximum {
			found[f] = n
		}
	}
	base, err := e.readBaseline(FileLengthBaseline, 1)
	if err != nil {
		return e.fail(lane, err)
	}
	problems := baseline.Compare(FileLengthBaseline, base, found)
	for i, p := range problems {
		problems[i] = strings.Replace(p, " found,", fmt.Sprintf(" lines, over %d,", maximum), 1)
	}
	return e.report(lane, problems, fmt.Sprintf("%d files within %d lines", checked, maximum))
}

func excluded(file, patterns string) bool {
	for _, pat := range strings.Split(patterns, ",") {
		if pat == "" {
			continue
		}
		target := path.Base(file)
		if strings.Contains(pat, "/") {
			target = file
		}
		if ok, _ := path.Match(pat, target); ok {
			return true
		}
	}
	return false
}

func lineCount(data []byte) int {
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}
