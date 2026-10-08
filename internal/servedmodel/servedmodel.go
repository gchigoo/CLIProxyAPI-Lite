// Package servedmodel keeps an in-memory, per-credential summary of the models
// upstreams report serving. It is fed by the native usage pipeline and exposed
// through the management auth-files listing.
package servedmodel

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const (
	// MaxPairsPerAuth bounds the requested/served pairs kept for one credential.
	MaxPairsPerAuth = 16
	// MaxAuths bounds the credentials tracked at once.
	MaxAuths = 1024
)

// Entry summarizes one requested/served model pair for a credential.
type Entry struct {
	RequestedModel string    `json:"requested_model"`
	ServedModel    string    `json:"served_model"`
	Substituted    bool      `json:"substituted"`
	Count          int64     `json:"count"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
}

type pairKey struct {
	requested string
	served    string
}

type pairState struct {
	entry Entry
	seq   uint64
}

type authSummary struct {
	pairs map[pairKey]*pairState
	seq   uint64
}

// Tracker aggregates served models per credential and implements coreusage.Plugin.
type Tracker struct {
	mu       sync.Mutex
	auths    map[string]*authSummary
	now      func() time.Time
	seq      uint64
	maxPairs int
	maxAuths int
}

// NewTracker returns an empty tracker. A nil clock uses time.Now.
func NewTracker(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{
		auths:    make(map[string]*authSummary),
		now:      now,
		maxPairs: MaxPairsPerAuth,
		maxAuths: MaxAuths,
	}
}

var defaultTracker = NewTracker(nil)

func init() {
	coreusage.RegisterNamedPlugin("served-model", defaultTracker)
}

// Default returns the tracker registered with the default usage manager.
func Default() *Tracker {
	return defaultTracker
}

// Snapshot returns the default tracker's entries for authID, newest first.
func Snapshot(authID string) []Entry {
	return defaultTracker.Snapshot(authID)
}

// HandleUsage records the served model of a usage record. Records without a
// credential or a reported model are ignored.
func (t *Tracker) HandleUsage(_ context.Context, record coreusage.Record) {
	if t == nil {
		return
	}
	authID := strings.TrimSpace(record.AuthID)
	served := strings.TrimSpace(record.ResponseModel)
	if authID == "" || served == "" {
		return
	}
	requested := strings.TrimSpace(thinking.ParseSuffix(strings.TrimSpace(record.Model)).ModelName)
	key := pairKey{requested: requested, served: served}
	now := t.now().UTC()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	summary := t.auths[authID]
	if summary == nil {
		if len(t.auths) >= t.maxAuths {
			t.evictOldestAuthLocked()
		}
		summary = &authSummary{pairs: make(map[pairKey]*pairState)}
		t.auths[authID] = summary
	}
	summary.seq = t.seq
	state := summary.pairs[key]
	if state == nil {
		if len(summary.pairs) >= t.maxPairs {
			evictOldestPair(summary)
		}
		state = &pairState{entry: Entry{
			RequestedModel: requested,
			ServedModel:    served,
			FirstSeen:      now,
		}}
		summary.pairs[key] = state
	}
	// The reporter judges substitution against the model it expected upstream,
	// which differs from Record.Model for mapped models.
	state.entry.Substituted = record.ResponseModelSubstituted
	state.entry.Count++
	state.entry.LastSeen = now
	state.seq = t.seq
}

// Snapshot returns copies of the entries for authID, most recently seen first.
func (t *Tracker) Snapshot(authID string) []Entry {
	if t == nil {
		return nil
	}
	authID = strings.TrimSpace(authID)
	t.mu.Lock()
	defer t.mu.Unlock()
	summary := t.auths[authID]
	if summary == nil || len(summary.pairs) == 0 {
		return nil
	}
	states := make([]*pairState, 0, len(summary.pairs))
	for _, state := range summary.pairs {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].seq > states[j].seq })
	out := make([]Entry, len(states))
	for i, state := range states {
		out[i] = state.entry
	}
	return out
}

func (t *Tracker) evictOldestAuthLocked() {
	var oldestID string
	var oldestSeq uint64
	for id, summary := range t.auths {
		if oldestID == "" || summary.seq < oldestSeq {
			oldestID, oldestSeq = id, summary.seq
		}
	}
	delete(t.auths, oldestID)
}

func evictOldestPair(summary *authSummary) {
	var oldestKey pairKey
	var oldestSeq uint64
	found := false
	for key, state := range summary.pairs {
		if !found || state.seq < oldestSeq {
			oldestKey, oldestSeq, found = key, state.seq, true
		}
	}
	delete(summary.pairs, oldestKey)
}
