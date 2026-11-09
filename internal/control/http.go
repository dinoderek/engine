package control

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxBody = 2 << 20

// Handler exposes one versioned command endpoint. Storage errors stay server-side.
func Handler(service Service, operatorToken string, logf func(string, ...any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/v1/command" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		a, err := service.Authenticate(token, operatorToken)
		if err != nil {
			httpFailure(w, err, logf)
			return
		}
		var req Request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&req); err != nil {
			httpFailure(w, requestProblem(err), logf)
			return
		}
		if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
			httpFailure(w, problem(400, "one JSON request is required"), logf)
			return
		}
		out, err := service.Execute(a, req)
		if err != nil {
			httpFailure(w, err, logf)
			return
		}
		if err = json.NewEncoder(w).Encode(out); err != nil && logf != nil {
			logf("write control response: %v", err)
		}
	})
}

// requestProblem is a request the server cannot decode. A field it does not
// know usually comes from a client newer than the server, so the error says
// to upgrade the server rather than leave a bare "invalid JSON".
func requestProblem(err error) error {
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return problem(400, "invalid request JSON: this server does not know the field %s, which a newer client sends; upgrade engine serve to the client's build (engine version)", field)
	}
	return problem(400, "invalid request JSON")
}

func httpFailure(w http.ResponseWriter, err error, logf func(string, ...any)) {
	status, message := 500, "control storage operation failed"
	var p *Problem
	if errors.As(err, &p) {
		status, message = p.Status, p.Message
	} else if logf != nil {
		logf("control operation: %v", err)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Client communicates only with its configured endpoint, with a bounded request.
type Client struct {
	URL, Token string
	HTTP       *http.Client
}

// Call retries an uncertain delivery once using the same idempotency key.
func (c Client) Call(ctx context.Context, r Request) (Response, error) {
	if _, err := Endpoint(c.URL); err != nil {
		return Response{}, err
	}
	if r.Key == "" {
		r.Key = Secret()
	}
	body, err := json.Marshal(r)
	if err != nil {
		return Response{}, err
	}
	var out Response
	for attempt := 0; attempt < 2; attempt++ {
		var retry bool
		out, retry, err = c.once(ctx, body)
		if err == nil || !retry || ctx.Err() != nil {
			return out, err
		}
	}
	return out, fmt.Errorf("%w (request key %s; retry with the same key)", err, r.Key)
}

func (c Client) once(ctx context.Context, body []byte) (Response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/v1/command", bytes.NewReader(body))
	if err != nil {
		return Response{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if c.HTTP != nil {
		client = *c.HTTP
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Second
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var failure struct{ Error string }
		if err = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&failure); err != nil || failure.Error == "" {
			failure.Error = http.StatusText(resp.StatusCode)
		}
		return Response{}, resp.StatusCode >= 500, problem(resp.StatusCode, "%s", failure.Error)
	}
	var out Response
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return out, true, fmt.Errorf("read control response: %w", err)
	}
	return out, false, nil
}

// Endpoint refuses embedded credentials, URL arguments and unsupported transports.
func Endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("server URL must be an http(s) origin without credentials or a path")
	}
	return u, nil
}

// ReadCredential refuses credentials exposed to other users.
func ReadCredential(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("credential must be a regular file readable only by its owner (chmod 600)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("credential must contain at least 32 non-whitespace characters")
	}
	return token, nil
}

func sameSecret(a, b string) bool {
	ah, bh := digest([]byte(a)), digest([]byte(b))
	return subtle.ConstantTimeCompare([]byte(ah), []byte(bh)) == 1
}
