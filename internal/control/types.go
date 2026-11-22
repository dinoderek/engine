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
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Version  int      `json:"version"`
	Repo     string   `json:"repo,omitempty"`
	Plan     string   `json:"plan,omitempty"`
	Title    string   `json:"title"`
	Body     string   `json:"body,omitempty"`
	Slug     string   `json:"slug,omitempty"`
	Remote   string   `json:"remote,omitempty"`
	State    string   `json:"state"`
	Depends  []string `json:"depends,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Harness  string   `json:"harness,omitempty"`
	Tier     string   `json:"tier,omitempty"`
	PR       string   `json:"pr,omitempty"`
	Attempt  string   `json:"attempt,omitempty"`
	// Coordinating and ApprovedVersion are a plan's run intent and the
	// version the operator approved; a task follows its plan's.
	Coordinating    bool `json:"coordinating,omitempty"`
	ApprovedVersion int  `json:"approved_version,omitempty"`
	// Status is a plan's PlanStatus in read responses. It is never stored.
	Status string `json:"status,omitempty"`
	// Question is a plan's oldest open question in read responses, however
	// it was asked. It is never stored.
	Question *UpdateRef `json:"question,omitempty"`
	// StoppedAt is when this plan's coordination last stopped: the
	// operator's stop, or a planner's second death without a task.
	StoppedAt time.Time `json:"stopped_at,omitzero"`
	// Held is a todo task the operator stopped: no pass places it until
	// the operator resumes it.
	Held bool `json:"held,omitempty"`
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
	// IdleSince is when the session was first seen idle in its current
	// spell: waiting, or busy only in a background job.
	IdleSince  time.Time `json:"idle_since,omitzero"`
	Notice     time.Time `json:"notice,omitempty"`
	ResetsAt   int64     `json:"resets_at,omitempty"`
	ResumedFor int64     `json:"resumed_for,omitempty"`
	ResumeAt   time.Time `json:"resume_at,omitempty"`
	// LimitedSince is when the session was first seen limited in its current
	// spell at a usage limit.
	LimitedSince time.Time `json:"limited_since,omitzero"`
	Seen         int       `json:"seen,omitempty"`
	FarFor       int64     `json:"far_for,omitempty"`
	StuckFor     int64     `json:"stuck_for,omitempty"`
	// LiveAt is the last coordinator pass that saw the attempt's window live.
	LiveAt time.Time `json:"live_at,omitzero"`
	// Reason is why the attempt ended: EndVanished, EndEarlyDeath or
	// EndFailed for a dead window, EndStopped for the operator's stop,
	// EndMoved for the operator's move, and empty for a session released
	// once its work was done.
	Reason string `json:"reason,omitempty"`
	// Early is set when the session died before claiming its task, or a
	// planner's before writing any task, whether that ended the attempt
	// early-death or failed; Tail is the end of its pane log then, when the
	// host showed one.
	Early bool   `json:"early,omitempty"`
	Tail  string `json:"tail,omitempty"`
	// Crashed is set when a session that claimed its item, or a planner that
	// wrote tasks, died alone: a death that counts toward DeathCap, whether
	// it ended the attempt vanished or failed. Its Tail is kept.
	Crashed bool `json:"crashed,omitempty"`
	// Limited is set when the session ended at a usage limit: its window
	// died with the run's limit marker set. ResetsAt is then that limit's
	// reset, or zero when unknown, and the attempt ended vanished.
	Limited bool `json:"limited,omitempty"`
	// PaneKey is the public key, in hex, of the run's pane key, which its
	// prompt hook signs the operator's pane input with.
	PaneKey string `json:"pane_key,omitempty"`
	// MoveHost and MoveHarness are where an attempt that ended moved sends
	// its task's next dispatch: empty MoveHost is any host but this
	// attempt's, empty MoveHarness the task's own choice. MovedBlind is set
	// when the operator moved it while the latest pass could not read its
	// host: its session may still run and push, so the task is dispatched
	// without waiting for that host.
	MoveHost    string `json:"move_host,omitempty"`
	MoveHarness string `json:"move_harness,omitempty"`
	MovedBlind  bool   `json:"moved_blind,omitempty"`
}

// MoveRequest is a task move's destination. Confirm is the operator's
// go-ahead to move off a host the latest pass could not read.
type MoveRequest struct {
	Host    string `json:"host,omitempty"`
	Harness string `json:"harness,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
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
	// Task is the task whose session asked a plan question.
	Task     string `json:"task,omitempty"`
	Snapshot *Item  `json:"snapshot,omitempty"`
	// Merge is a merge request's details; only merge-request updates carry it.
	Merge *MergeRequest `json:"merge,omitempty"`
	// Left is what a cleaned update says the session could not remove.
	Left string `json:"left,omitempty"`
	// Choices are a question's answers to pick from; Recommended, when set,
	// is the asker's pick, an index into Choices from 0; Class is its
	// hard-stop class (HardStopClasses). Only a question carries them.
	Choices     []string `json:"choices,omitempty"`
	Recommended *int     `json:"recommended,omitempty"`
	Class       string   `json:"class,omitempty"`
}

// QueueEntry is a task's place in its repository's merge queue, from its
// merge request until the merge, or until it leaves the queue. It belongs to
// the task, not the attempt, so it outlives a vanished session.
type QueueEntry struct {
	Task string `json:"task"`
	Repo string `json:"repo"`
	// Request is the task's newest merge-request update; PR and Head are its.
	Request int    `json:"request"`
	PR      string `json:"pr"`
	Head    string `json:"head"`
	// Seq orders the queue: the request that created the entry.
	Seq int `json:"seq"`
	// Phase is QueueWaiting, QueueCheck (the head, merged once current and
	// green) or QueueRefresh (the head, told to refresh).
	Phase  string    `json:"phase"`
	HeadAt time.Time `json:"head_at,omitzero"`
	TurnAt time.Time `json:"turn_at,omitzero"`
	// CheckedAt is the head's last host call; zero calls it at once.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// WaitSince is the head's first reading of unknown mergeability.
	WaitSince time.Time `json:"wait_since,omitzero"`
	// FailSince is the head's first unreachable host call; Failures counts
	// its host's gh failures in a row.
	FailSince time.Time `json:"fail_since,omitzero"`
	Failures  int       `json:"failures,omitempty"`
	// Reason is the last thing the queue said about the entry.
	Reason string `json:"reason,omitempty"`
}

// The merge queue's phases.
const (
	QueueWaiting = "queued"
	QueueCheck   = "check"
	QueueRefresh = "refresh"
)

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
	// Before bounds ack-all to updates numbered below it; 0 is no bound.
	Before  int    `json:"before,omitempty"`
	Attempt string `json:"attempt,omitempty"`
	State   string `json:"state,omitempty"`
	// Merge is a merge-request's PR, head, method and verdict.
	Merge *MergeRequest `json:"merge,omitempty"`
	// Pane is how a pane-input post's prompt hook vouches for it.
	Pane *PaneInput `json:"pane,omitempty"`
	// Move is a task-move's destination; every other operation refuses it.
	Move *MoveRequest `json:"move,omitempty"`
	// Choices, Recommended and Class are an ask's (Update); every other
	// operation refuses them.
	Choices     []string `json:"choices,omitempty"`
	Recommended *int     `json:"recommended,omitempty"`
	Class       string   `json:"class,omitempty"`
}

// Response contains one operation's result. Credentials are only returned at dispatch.
type Response struct {
	Item       *Item        `json:"item,omitempty"`
	Items      []Item       `json:"items,omitempty"`
	Updates    []Update     `json:"updates,omitempty"`
	Attempt    *Attempt     `json:"attempt,omitempty"`
	Attempts   []Attempt    `json:"attempts,omitempty"`
	Queue      []QueueEntry `json:"queue,omitempty"`
	Sessions   []SessionRow `json:"sessions,omitempty"`
	Credential string       `json:"credential,omitempty"`
	// Fleet is the host-status op's reply.
	Fleet *HostStatus `json:"fleet,omitempty"`
}

type receipt struct {
	Digest   string   `json:"digest"`
	Response Response `json:"response"`
}

// State is a consistent database snapshot used by one transaction.
type State struct {
	Items    map[string]Item       `json:"items"`
	Attempts map[string]Attempt    `json:"attempts"`
	Updates  []Update              `json:"updates"`
	Queue    map[string]QueueEntry `json:"queue"`
	Auth     map[string]Actor      `json:"auth"`
	Receipts map[string]receipt    `json:"receipts"`
	Next     int                   `json:"next"`
}

func emptyState() State {
	return State{Items: map[string]Item{}, Attempts: map[string]Attempt{}, Queue: map[string]QueueEntry{}, Auth: map[string]Actor{}, Receipts: map[string]receipt{}, Next: 1}
}

func (s *State) id(prefix string) string {
	id := fmt.Sprintf("%s%d", prefix, s.Next)
	s.Next++
	return id
}

func (s *State) update(a Actor, r Request, kind, body string, now time.Time) Update {
	u := Update{ID: len(s.Updates) + 1, Item: r.ID, Author: a.Name, At: now.UTC(), Kind: kind, Body: body,
		Recipient: r.Recipient, ReplyTo: r.ReplyTo, RequiresReply: r.RequiresReply,
		Choices: r.Choices, Recommended: r.Recommended, Class: r.Class}
	if item, ok := s.Items[r.ID]; ok {
		u.Snapshot = &item
	}
	s.Updates = append(s.Updates, u)
	return u
}

// Secret generates an opaque credential.
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
// Reason, when set, is a stable code a caller can tell the refusal by; the
// response carries it beside the message.
type Problem struct {
	Status  int
	Message string
	Reason  string
}

// ReasonReconcileRefused is attempt reconcile's refusal: the host read did
// not confirm the execution's session gone.
const ReasonReconcileRefused = "reconcile-refused"

// ReasonMoveUnconfirmed is a task move's refusal while the latest pass could
// not read the task's host: the operator must confirm it.
const ReasonMoveUnconfirmed = "move-unconfirmed"

func (e *Problem) Error() string {
	if e.Reason != "" {
		return e.Reason + ": " + e.Message
	}
	return e.Message
}
func problem(status int, format string, args ...any) error {
	return &Problem{Status: status, Message: fmt.Sprintf(format, args...)}
}
