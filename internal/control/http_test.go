package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthenticatedHTTPAndUncertainDeliveryRetry(t *testing.T) {
	s := service(t)
	token := Secret()
	handler := Handler(s, token, t.Logf)
	lost := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lost {
			lost = false
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":"response lost"}`)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := Client{URL: server.URL, Token: token}
	out, err := client.Call(context.Background(), Request{Op: "create", Item: Item{Kind: "repo", Title: "HTTP", Slug: "x/y", Remote: "remote"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Item == nil {
		t.Fatal("retry lost original response")
	}
	st, _ := s.Store.Snapshot()
	if len(st.Items) != 1 || len(st.Updates) != 1 {
		t.Fatal("retry duplicated write")
	}
	client.Token = "invalid"
	if _, err = client.Call(context.Background(), Request{Op: "list"}); err == nil {
		t.Fatal("unauthenticated read")
	}
	client.Token = token
	if _, err = client.Call(context.Background(), Request{Op: "get", ID: "missing"}); err == nil {
		t.Fatal("HTTP failure ignored")
	}
}

func TestHTTPValidationAndStorageErrors(t *testing.T) {
	s := service(t)
	token := Secret()
	h := Handler(s, token, t.Logf)
	cases := []struct {
		method, path, body, token string
		status                    int
	}{
		{"GET", "/v1/command", "", token, 405},
		{"POST", "/missing", "{}", token, 404},
		{"POST", "/v1/command", "{}", "", 401},
		{"POST", "/v1/command", "bad", token, 400},
		{"POST", "/v1/command", `{"op":"list","author":"operator"}`, token, 400},
		{"POST", "/v1/command", "{} {}", token, 400},
		{"POST", "/v1/command", `{"op":"unknown","key":"x"}`, token, 400},
		{"POST", "/v1/command", strings.Repeat("x", maxBody+1), token, 400},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status, tc.body[:min(len(tc.body), 12)]), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("%d: %s", rr.Code, rr.Body.String())
			}
		})
	}
	// A field the server does not know names it and the upgrade; other bad
	// JSON stays plain.
	for body, want := range map[string]string{
		`{"op":"list","future":{}}`: `this server does not know the field \"future\", which a newer client sends; upgrade engine serve`,
		"bad":                       `{"error":"invalid request JSON"}`,
	} {
		req := httptest.NewRequest("POST", "/v1/command", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("%s: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	_ = s.Store.Close()
	req := httptest.NewRequest("POST", "/v1/command", strings.NewReader(`{"op":"list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 500 || strings.Contains(rr.Body.String(), "closed") {
		t.Fatalf("storage error leaked: %s", rr.Body.String())
	}
}

func TestClientRefusesRedirectsAndMalformedResponses(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if _, err := (Client{URL: redirect.URL, Token: Secret()}).Call(context.Background(), Request{Op: "list"}); err == nil || reached {
		t.Fatal("credential followed redirect")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "invalid") }))
	defer bad.Close()
	if _, err := (Client{URL: bad.URL}).Call(context.Background(), Request{Op: "list"}); err == nil {
		t.Fatal("malformed reply accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Client{URL: bad.URL}).Call(ctx, Request{Op: "list"}); err == nil {
		t.Fatal("cancellation ignored")
	}
	for _, url := range []string{"", "file:///tmp/db", "http://user:secret@localhost", "https://example.test/path", "https://example.test?a=b"} {
		if _, err := Endpoint(url); err == nil {
			t.Fatal(url)
		}
	}
}

func TestCredentialsAndWorkerAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	token := Secret()
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCredential(path)
	if err != nil || got != token {
		t.Fatal(got, err)
	}
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadCredential(path); err == nil {
		t.Fatal("public token accepted")
	}
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadCredential(path); err == nil {
		t.Fatal("short token accepted")
	}
	s := service(t)
	if _, err = s.Authenticate("", ""); err == nil {
		t.Fatal("empty auth")
	}
	if _, err = s.Authenticate("invalid", ""); err == nil {
		t.Fatal("invalid auth")
	}
	b, _ := json.Marshal(Response{Credential: token})
	if !strings.Contains(string(b), token) {
		t.Fatal("dispatch token response omitted")
	}
}
