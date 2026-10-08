package servedmodel

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

func newTestTracker() (*Tracker, *stepClock) {
	clock := &stepClock{now: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	return NewTracker(clock.Now), clock
}

func record(authID, model, served string) coreusage.Record {
	return coreusage.Record{AuthID: authID, Model: model, ResponseModel: served}
}

// substitutedRecord is a record the usage reporter marked as a substitution.
func substitutedRecord(authID, model, served string) coreusage.Record {
	rec := record(authID, model, served)
	rec.ResponseModelSubstituted = true
	return rec
}

func TestTrackerAggregatesPairsAndFlagsSubstitution(t *testing.T) {
	tracker, _ := newTestTracker()
	ctx := context.Background()
	tracker.HandleUsage(ctx, record("auth-1", "gpt-5.6-sol", "gpt-5.6-sol"))
	tracker.HandleUsage(ctx, substitutedRecord("auth-1", "gpt-5.6-sol(high)", "gpt-5.5-mini"))
	tracker.HandleUsage(ctx, substitutedRecord("auth-1", "gpt-5.6-sol", "gpt-5.5-mini"))
	tracker.HandleUsage(ctx, record("auth-1", "gpt-5.6-terra", "gpt-5.6-terra-2026-05-13"))

	got := tracker.Snapshot("auth-1")
	if len(got) != 3 {
		t.Fatalf("Snapshot() returned %d entries, want 3: %+v", len(got), got)
	}
	terra, mini, sol := got[0], got[1], got[2]
	if terra.RequestedModel != "gpt-5.6-terra" || terra.ServedModel != "gpt-5.6-terra-2026-05-13" || terra.Substituted {
		t.Fatalf("dated snapshot entry = %+v, want unsubstituted terra pair", terra)
	}
	if mini.RequestedModel != "gpt-5.6-sol" || mini.ServedModel != "gpt-5.5-mini" || !mini.Substituted || mini.Count != 2 {
		t.Fatalf("substituted entry = %+v, want sol->mini substituted with count 2", mini)
	}
	if !mini.FirstSeen.Before(mini.LastSeen) {
		t.Fatalf("substituted entry FirstSeen %v should be before LastSeen %v", mini.FirstSeen, mini.LastSeen)
	}
	if sol.ServedModel != "gpt-5.6-sol" || sol.Substituted || sol.Count != 1 {
		t.Fatalf("matching entry = %+v, want sol->sol unsubstituted count 1", sol)
	}
	if mini.LastSeen.Location() != time.UTC {
		t.Fatalf("LastSeen location = %v, want UTC", mini.LastSeen.Location())
	}
}

func TestTrackerMirrorsReporterSubstitutionFlag(t *testing.T) {
	tracker, _ := newTestTracker()
	ctx := context.Background()
	// A mapped upstream model (for example Kimi's canonical ID) is not flagged by the reporter.
	tracker.HandleUsage(ctx, record("auth-1", "kimi-k2.7-code", "kimi-for-coding"))
	got := tracker.Snapshot("auth-1")
	if len(got) != 1 || got[0].Substituted {
		t.Fatalf("Snapshot() = %+v, want one unsubstituted entry", got)
	}
	tracker.HandleUsage(ctx, substitutedRecord("auth-1", "kimi-k2.7-code", "kimi-for-coding"))
	if got = tracker.Snapshot("auth-1"); len(got) != 1 || !got[0].Substituted || got[0].Count != 2 {
		t.Fatalf("Snapshot() = %+v, want the latest reporter flag with count 2", got)
	}
}

func TestTrackerIgnoresRecordsWithoutAuthOrServedModel(t *testing.T) {
	tracker, _ := newTestTracker()
	ctx := context.Background()
	tracker.HandleUsage(ctx, record("", "gpt-5.6-sol", "gpt-5.5-mini"))
	tracker.HandleUsage(ctx, record("auth-1", "gpt-5.6-sol", "  "))
	if got := tracker.Snapshot("auth-1"); got != nil {
		t.Fatalf("Snapshot() = %+v, want nil", got)
	}
	if got := tracker.Snapshot(""); got != nil {
		t.Fatalf("Snapshot(empty) = %+v, want nil", got)
	}
}

func TestTrackerCountsFailedRecordsThatReportAModel(t *testing.T) {
	tracker, _ := newTestTracker()
	rec := substitutedRecord("auth-1", "gpt-5.6-sol", "gpt-5.5-mini")
	rec.Failed = true
	tracker.HandleUsage(context.Background(), rec)
	got := tracker.Snapshot("auth-1")
	if len(got) != 1 || got[0].Count != 1 || !got[0].Substituted {
		t.Fatalf("Snapshot() = %+v, want one substituted entry", got)
	}
}

func TestTrackerEvictsOldestPairPerAuth(t *testing.T) {
	tracker, _ := newTestTracker()
	ctx := context.Background()
	for i := 0; i < MaxPairsPerAuth; i++ {
		tracker.HandleUsage(ctx, record("auth-1", "requested", fmt.Sprintf("served-%02d", i)))
	}
	// Touch the oldest pair so the second one becomes the eviction candidate.
	tracker.HandleUsage(ctx, record("auth-1", "requested", "served-00"))
	tracker.HandleUsage(ctx, record("auth-1", "requested", "served-new"))

	got := tracker.Snapshot("auth-1")
	if len(got) != MaxPairsPerAuth {
		t.Fatalf("Snapshot() returned %d entries, want %d", len(got), MaxPairsPerAuth)
	}
	seen := make(map[string]bool, len(got))
	for _, entry := range got {
		seen[entry.ServedModel] = true
	}
	if seen["served-01"] {
		t.Fatal("least recently seen pair served-01 was not evicted")
	}
	if !seen["served-00"] || !seen["served-new"] {
		t.Fatalf("recent pairs missing after eviction: %+v", got)
	}
	if got[0].ServedModel != "served-new" || got[1].ServedModel != "served-00" {
		t.Fatalf("Snapshot() order = %s, %s; want served-new, served-00", got[0].ServedModel, got[1].ServedModel)
	}
}

func TestTrackerEvictsOldestAuth(t *testing.T) {
	tracker, _ := newTestTracker()
	tracker.maxAuths = 3
	ctx := context.Background()
	tracker.HandleUsage(ctx, record("auth-a", "m", "m"))
	tracker.HandleUsage(ctx, record("auth-b", "m", "m"))
	tracker.HandleUsage(ctx, record("auth-c", "m", "m"))
	tracker.HandleUsage(ctx, record("auth-a", "m", "m"))
	tracker.HandleUsage(ctx, record("auth-d", "m", "m"))

	if got := tracker.Snapshot("auth-b"); got != nil {
		t.Fatalf("auth-b should have been evicted, got %+v", got)
	}
	for _, id := range []string{"auth-a", "auth-c", "auth-d"} {
		if got := tracker.Snapshot(id); len(got) != 1 {
			t.Fatalf("Snapshot(%s) = %+v, want one entry", id, got)
		}
	}
}

func TestTrackerSnapshotReturnsCopies(t *testing.T) {
	tracker, _ := newTestTracker()
	tracker.HandleUsage(context.Background(), record("auth-1", "m", "n"))
	first := tracker.Snapshot("auth-1")
	first[0].Count = 99
	if got := tracker.Snapshot("auth-1"); got[0].Count != 1 {
		t.Fatalf("Snapshot() shares state with callers: count = %d", got[0].Count)
	}
}

func TestTrackerConcurrentUseIsSafe(t *testing.T) {
	tracker, _ := newTestTracker()
	ctx := context.Background()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tracker.HandleUsage(ctx, record(fmt.Sprintf("auth-%d", worker%3), "gpt-5.6-sol", fmt.Sprintf("served-%d", i%20)))
				_ = tracker.Snapshot(fmt.Sprintf("auth-%d", i%3))
			}
		}(worker)
	}
	wg.Wait()
	for i := 0; i < 3; i++ {
		got := tracker.Snapshot(fmt.Sprintf("auth-%d", i))
		if len(got) == 0 || len(got) > MaxPairsPerAuth {
			t.Fatalf("Snapshot(auth-%d) has %d entries, want 1..%d", i, len(got), MaxPairsPerAuth)
		}
	}
}

type publishedSignal struct {
	authID string
	seen   chan struct{}
}

func (p *publishedSignal) HandleUsage(_ context.Context, record coreusage.Record) {
	if record.AuthID == p.authID {
		select {
		case p.seen <- struct{}{}:
		default:
		}
	}
}

type noopPlugin struct{}

func (noopPlugin) HandleUsage(context.Context, coreusage.Record) {}

func TestDefaultTrackerReceivesPublishedUsage(t *testing.T) {
	const authID = "served-model-default-tracker-test"
	// The usage manager invokes plugins in registration order on one worker, so
	// this signal fires after the default tracker handled the same record.
	signal := &publishedSignal{authID: authID, seen: make(chan struct{}, 1)}
	coreusage.RegisterNamedPlugin(t.Name(), signal)
	t.Cleanup(func() { coreusage.RegisterNamedPlugin(t.Name(), noopPlugin{}) })

	coreusage.PublishRecord(context.Background(), coreusage.Record{AuthID: authID, Model: "gpt-5.6-sol", ResponseModel: "gpt-5.5-mini", ResponseModelSubstituted: true, RequestedAt: time.Now()})
	select {
	case <-signal.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the usage record to be dispatched")
	}
	if got := Snapshot(authID); len(got) != 1 || !got[0].Substituted {
		t.Fatalf("default tracker snapshot = %+v, want one substituted entry", got)
	}
}
