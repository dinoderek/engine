package ui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/control"
)

//go:embed static
var static embed.FS

// assets are the embedded files the page loads, by route.
var assets = map[string]struct{ file, kind string }{
	"/":            {"static/index.html", "text/html; charset=utf-8"},
	"/app.css":     {"static/app.css", "text/css; charset=utf-8"},
	"/app.js":      {"static/app.js", "text/javascript; charset=utf-8"},
	"/dom.js":      {"static/dom.js", "text/javascript; charset=utf-8"},
	"/panel.js":    {"static/panel.js", "text/javascript; charset=utf-8"},
	"/sessions.js": {"static/sessions.js", "text/javascript; charset=utf-8"},
}

const policy = "default-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// handler serves the page and its fixed read routes, same-origin, to loopback
// clients that name a loopback host.
func handler(up *upstream) http.Handler {
	mux := http.NewServeMux()
	for route := range assets {
		pattern := "GET " + route
		if route == "/" {
			pattern += "{$}"
		}
		mux.HandleFunc(pattern, asset)
	}
	mux.HandleFunc("GET /api/plans", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		env, status := up.plans(r.Context(), Filter{Repo: q.Get("repo"), State: q.Get("state"), Query: q.Get("q")}, q.Get("cached") == "1")
		reply(w, status, env)
	})
	mux.HandleFunc("GET /api/plans/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !itemID(id, 'p') {
			reply(w, 404, Envelope{Error: "unknown plan"})
			return
		}
		env, status := up.plan(r.Context(), id)
		reply(w, status, env)
	})
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		env, status := up.sessions(r.Context())
		reply(w, status, env)
	})
	mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !panelItem(id) {
			reply(w, 404, Envelope{Error: "unknown item"})
			return
		}
		env, status := up.item(r.Context(), id)
		reply(w, status, env)
	})
	mux.HandleFunc("GET /api/items/{id}/thread", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !panelItem(id) {
			reply(w, 404, Envelope{Error: "unknown item"})
			return
		}
		after, aok := count(r.URL.Query().Get("after"))
		anchor, nok := count(r.URL.Query().Get("anchor"))
		if !aok || !nok {
			reply(w, 400, Envelope{Error: "after and anchor must be update sequences"})
			return
		}
		env, status := up.thread(r.Context(), id, after, anchor)
		reply(w, status, env)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", policy)
		if !LoopbackHost(r.Host) {
			http.Error(w, "the dashboard answers only to a loopback host name", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "the dashboard is read-only", http.StatusMethodNotAllowed)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func asset(w http.ResponseWriter, r *http.Request) {
	a := assets[r.URL.Path]
	b, err := fs.ReadFile(static, a.file)
	if err != nil {
		http.Error(w, "missing asset", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", a.kind)
	_, _ = w.Write(b)
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// itemID accepts an engine ID with the given prefix, such as p7.
func itemID(id string, prefix byte) bool {
	return len(id) > 1 && id[0] == prefix && control.Number(id) > 0
}

// panelItem accepts the ID of a plan or a task, the items with a thread.
func panelItem(id string) bool { return itemID(id, 'p') || itemID(id, 't') }

// count parses an optional non-negative sequence; empty is zero.
func count(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0 && n < 1<<31
}

// LoopbackHost reports whether a Host header or listen address names this
// machine's loopback interface. It refuses other names, which defeats DNS
// rebinding, and accepts any port, so an SSH tunnel works.
func LoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
