package control

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

func (s Service) completeExternal(st *State, a Actor, r Request) (Response, error) {
	if err := operator(a); err != nil {
		return Response{}, err
	}
	item, ok := st.Items[r.ID]
	if !ok {
		return Response{}, problem(404, "unknown task")
	}
	if item.Kind != "task" {
		return Response{}, problem(400, "external completion requires a task")
	}
	if item.Version != r.Version {
		return Response{}, problem(409, "version is %d", item.Version)
	}
	if active(st, item) || !slices.Contains([]string{"todo", "failed"}, item.State) {
		return Response{}, problem(409, "external completion requires an inactive, unfinished task without a hard stop")
	}
	if PendingQuestion(st, item.ID) > 0 {
		return Response{}, problem(409, "operator question is unresolved")
	}
	if !completionPR(r.Item.PR, st.Items[item.Repo].Slug) {
		return Response{}, problem(400, "a GitHub PR URL for this repository is required")
	}
	item.State, item.PR = "done", r.Item.PR
	item.Version++
	st.Items[item.ID] = item
	st.update(a, r, "complete-external", fmt.Sprintf("done: %s (version %d)", item.PR, item.Version), s.now())
	return Response{Item: &item}, nil
}

func completionPR(raw, slug string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	prefix := "/" + slug + "/pull/"
	if !strings.HasPrefix(u.Path, prefix) {
		return false
	}
	number := strings.TrimPrefix(u.Path, prefix)
	n, err := strconv.Atoi(number)
	return err == nil && n > 0 && strconv.Itoa(n) == number && u.RawPath == ""
}
