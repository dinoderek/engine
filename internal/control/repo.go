package control

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
)

// editRepo replaces a repository's requires, as a card edit does a card's: the host capabilities every one
// of its tasks needs. Its identity (title, slug, remote) never changes, so a
// repository edit carries nothing else.
func (s Service) editRepo(tx *Tx, a Actor, r Request, old Item) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	rest := r.Item
	rest.Kind, rest.Requires = "", nil
	if !reflect.DeepEqual(rest, Item{}) {
		return Response{}, problem(400, "repository identity is immutable: an edit changes only its requires")
	}
	old.Requires = r.Item.Requires
	old.Version++
	tx.PutItem(old)
	tx.AppendUpdate(a, r, "edited", "requires: "+cmp.Or(strings.Join(old.Requires, ", "), "none"), s.now())
	return Response{Item: &old}, nil
}

// Requires are the host capabilities a task of repo needs: the repository's,
// then the task's own, each once.
func Requires(repo, task Item) []string {
	var out []string
	for _, c := range slices.Concat(repo.Requires, task.Requires) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}
