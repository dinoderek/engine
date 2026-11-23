package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// persistentDocs are the repository's Markdown files outside design/, whose
// drafts are ephemeral, keyed by their path from the repository root.
func persistentDocs(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	docs := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() && (rel == "design" || strings.HasPrefix(d.Name(), ".")) && rel != "." {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		b, err := os.ReadFile(path)
		docs[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

// sqlite3Reason is why block may name sqlite3: a Codex host's reader, the
// tests' tools, or the offline status check that reads the database file
// when the server does not answer. Anything else would be a server
// prerequisite, and the server needs none.
func sqlite3Reason(doc, block string) bool {
	return strings.Contains(block, "Codex") || doc == "docs/testing-strategy.md" ||
		strings.Contains(block, "database check is unknown without `sqlite3` on the server")
}

// No persistent doc lists sqlite3 as something the server needs: each
// paragraph, list item or table row that names it names it for a Codex host,
// the tests or the offline status check.
func TestNoDocListsSQLite3AsAServerPrerequisite(t *testing.T) {
	docs := persistentDocs(t)
	if _, ok := docs["docs/operator-setup.md"]; !ok {
		t.Fatalf("the docs were not found: %d files", len(docs))
	}
	named := 0
	for doc, text := range docs {
		for _, block := range strings.Split(text, "\n\n") {
			for _, item := range strings.Split(block, "\n- ") {
				if !strings.Contains(item, "sqlite3") {
					continue
				}
				named++
				if !sqlite3Reason(doc, item) {
					t.Errorf("%s names sqlite3 outside a Codex host, the tests or the offline status check:\n%s", doc, item)
				}
			}
		}
	}
	if named == 0 {
		t.Error("no doc names sqlite3, not even for Codex hosts")
	}
}
