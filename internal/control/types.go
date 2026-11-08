// Package control owns engine's server records and transactional operations.
package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Item is a repository, a feature-level plan, or a task. Version protects edits.
type Item struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Version         int      `json:"version"`
	Repo            string   `json:"repo,omitempty"`
	Plan            string   `json:"plan,omitempty"`
	Title           string   `json:"title"`
	Body            string   `json:"body,omitempty"`
	Slug            string   `json:"slug,omitempty"`
	Remote          string   `json:"remote,omitempty"`
	State           string   `json:"state"`
	Depends         []string `json:"depends,omitempty"`
	Requires        []string `json:"requires,omitempty"`
	Harness         string   `json:"harness,omitempty"`
	Tier            string   `json:"tier,omitempty"`
	PR              string   `json:"pr,omitempty"`
	Attempt         string   `json:"attempt,omitempty"`
	Coordinating    bool     `json:"coordinating,omitempty"`
	ApprovedVersion int      `json:"approved_version,omitempty"`
	// StoppedAt is when the operator last stopped this item's coordination.
	StoppedAt time.Time `json:"stopped_at,omitzero"`
}

// Attempt persists a dispatch before the host is contacted.
type Attempt struct {
	ID           string    `json:"id"`
	Item         string    `json:"item"`
	Host         string    `json:"host"`
	Harness      string    `json:"harness"`
	RunID        string    `json:"run_id"`
	State        string    `json:"state"`
	Window       string    `json:"window,omitempty"`
	Started      time.Time `json:"started"`
	Ended        time.Time `json:"ended,omitempty"`
	WaitingSince time.Time `json:"waiting_since,omitempty"`
	Notice       time.Time `json:"notice,omitempty"`
	ResetsAt     int64     `json:"resets_at,omitempty"`
	ResumedFor   int64     `json:"resumed_for,omitempty"`
	ResumeAt     time.Time `json:"resume_at,omitempty"`
	Seen         int       `json:"seen,omitempty"`
	FarFor       int64     `json:"far_for,omitempty"`
	StuckFor     int64     `json:"stuck_for,omitempty"`
	// LiveAt is the last coordinator pass that saw the attempt's window live.
	LiveAt time.Time `json:"live_at,omitzero"`
	// Reason is why a dead window ended the attempt: EndVanished or EndFailed.
	Reason string `json:"reason,omitempty"`
}

// Update is immutable history; acknowledgement and resolution are separate records.
type Update struct {
	ID            int       `json:"id"`
	Item          string    `json:"item,omitempty"`
	Author        string    `json:"author"`
	At            time.Time `json:"at"`
	Kind          string    `json:"kind"`
	Body          string    `json:"body"`
	Recipient     string    `json:"recipient,omitempty"`
	ReplyTo       int       `json:"reply_to,omitempty"`
	RequiresReply bool      `json:"requires_reply,omitempty"`
	Snapshot      *Item     `json:"snapshot,omitempty"`
}

// Lock has no expiry. Only its owner can release it without recovery.
type Lock struct {
	Repo     string    `json:"repo"`
	Attempt  string    `json:"attempt"`
	Token    string    `json:"token"`
	Acquired time.Time `json:"acquired"`
}

// Actor is derived from a credential, never from a request's author field.
type Actor struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Attempt string `json:"attempt,omitempty"`
}

// Request is the versioned command envelope shared by HTTP and the CLI.
type Request struct {
	Op            string `json:"op"`
	Key           string `json:"key,omitempty"`
	ID            string `json:"id,omitempty"`
	Version       int    `json:"version,omitempty"`
	Item          Item   `json:"item,omitempty"`
	Body          string `json:"body,omitempty"`
	Recipient     string `json:"recipient,omitempty"`
	ReplyTo       int    `json:"reply_to,omitempty"`
	RequiresReply bool   `json:"requires_reply,omitempty"`
	After         int    `json:"after,omitempty"`
	Attempt       string `json:"attempt,omitempty"`
	Token         string `json:"token,omitempty"`
	State         string `json:"state,omitempty"`
}

// Response contains one operation's result. Credentials are only returned at dispatch.
type Response struct {
	Item       *Item        `json:"item,omitempty"`
	Items      []Item       `json:"items,omitempty"`
	Updates    []Update     `json:"updates,omitempty"`
	Attempt    *Attempt     `json:"attempt,omitempty"`
	Attempts   []Attempt    `json:"attempts,omitempty"`
	Lock       *Lock        `json:"lock,omitempty"`
	Sessions   []SessionRow `json:"sessions,omitempty"`
	Credential string       `json:"credential,omitempty"`
}

type receipt struct {
	Digest   string   `json:"digest"`
	Response Response `json:"response"`
}

// State is a consistent database snapshot used by one transaction.
type State struct {
	Items    map[string]Item    `json:"items"`
	Attempts map[string]Attempt `json:"attempts"`
	Updates  []Update           `json:"updates"`
	Locks    map[string]Lock    `json:"locks"`
	Auth     map[string]Actor   `json:"auth"`
	Receipts map[string]receipt `json:"receipts"`
	Next     int                `json:"next"`
}

func emptyState() State {
	return State{Items: map[string]Item{}, Attempts: map[string]Attempt{}, Locks: map[string]Lock{}, Auth: map[string]Actor{}, Receipts: map[string]receipt{}, Next: 1}
}

func (s *State) id(prefix string) string {
	id := fmt.Sprintf("%s%d", prefix, s.Next)
	s.Next++
	return id
}

func (s *State) update(a Actor, r Request, kind, body string, now time.Time) Update {
	u := Update{ID: len(s.Updates) + 1, Item: r.ID, Author: a.Name, At: now.UTC(), Kind: kind, Body: body,
		Recipient: r.Recipient, ReplyTo: r.ReplyTo, RequiresReply: r.RequiresReply}
	if item, ok := s.Items[r.ID]; ok {
		u.Snapshot = &item
	}
	s.Updates = append(s.Updates, u)
	return u
}

// Secret generates an opaque credential or lock token.
func Secret() string { return hex.EncodeToString(randomBytes()) }
func randomBytes() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func digest(b []byte) string   { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func fingerprint(v any) string { b, _ := json.Marshal(v); return digest(b) }

// Problem carries a stable HTTP status without exposing storage errors.
type Problem struct {
	Status  int
	Message string
}

func (e *Problem) Error() string { return e.Message }
func problem(status int, format string, args ...any) error {
	return &Problem{status, fmt.Sprintf(format, args...)}
}
