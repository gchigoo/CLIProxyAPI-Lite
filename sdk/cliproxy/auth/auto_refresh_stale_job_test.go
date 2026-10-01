package auth

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func runIssue6199Worker(t *testing.T, loop *authAutoRefreshLoop) {
	t.Helper()
	ctx, cancelCtx := context.WithCancel(context.Background())
	t.Cleanup(cancelCtx)
	go loop.worker(ctx)
	synctest.Wait()
}

func TestAutoRefreshQueuedJobSkipsTerminalUnauthorizedAuth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executor := &countingRefreshExecutor{id: issue6199RefreshProvider}
		manager, loop := newIssue6199RefreshLoop(executor)
		now := time.Now()
		registerIssue6199ExpiredAuth(t, manager, "terminal", now)
		loop.rebuild(now)
		loop.handleDue(context.Background(), now)
		if got := len(loop.jobs); got != 1 {
			t.Fatalf("queued jobs = %d, want 1", got)
		}

		// A request-time refresh fails with a terminal 401 while the job is queued.
		manager.mu.Lock()
		auth := manager.auths["terminal"]
		auth.Unavailable = true
		auth.Status = StatusError
		auth.LastError = &Error{Code: "unauthorized", Message: "unauthorized", HTTPStatus: http.StatusUnauthorized}
		auth.NextRefreshAfter = time.Time{}
		manager.mu.Unlock()

		runIssue6199Worker(t, loop)
		if got := executor.refreshCalls.Load(); got != 0 {
			t.Fatalf("queued job refreshed a terminal unauthorized auth %d times, want 0", got)
		}
	})
}

func TestAutoRefreshQueuedJobSkipsAuthRefreshedAfterQueueing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executor := &countingRefreshExecutor{id: issue6199RefreshProvider}
		manager, loop := newIssue6199RefreshLoop(executor)
		now := time.Now()
		registerIssue6199ExpiredAuth(t, manager, "refreshed", now)
		loop.rebuild(now)
		loop.handleDue(context.Background(), now)

		// A request-time refresh succeeds after the job was queued.
		manager.mu.Lock()
		manager.auths["refreshed"].LastRefreshedAt = now.Add(time.Nanosecond)
		manager.mu.Unlock()

		runIssue6199Worker(t, loop)
		if got := executor.refreshCalls.Load(); got != 0 {
			t.Fatalf("queued job refreshed an already refreshed auth %d times, want 0", got)
		}
		manager.mu.RLock()
		pending := len(manager.refreshJobs)
		manager.mu.RUnlock()
		if pending != 0 {
			t.Fatalf("refresh jobs after skipped job = %d, want 0", pending)
		}
	})
}

func TestAutoRefreshStaleQueuedJobDoesNotBlockReplacementRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executor := &countingRefreshExecutor{id: issue6199RefreshProvider}
		manager, loop := newIssue6199RefreshLoop(executor)
		now := time.Now()
		registerIssue6199ExpiredAuth(t, manager, "replaced", now)
		loop.rebuild(now)
		loop.handleDue(context.Background(), now)
		manager.Remove(context.Background(), "replaced")
		registerIssue6199ExpiredAuth(t, manager, "replaced", now)

		// The replacement queues its own job while the stale one is still queued.
		loop.applyDirty(now)
		loop.handleDue(context.Background(), now)
		if got := len(loop.jobs); got != 2 {
			t.Fatalf("queued jobs after replacement = %d, want stale and replacement jobs", got)
		}

		runIssue6199Worker(t, loop)
		if got := executor.refreshCalls.Load(); got != 1 {
			t.Fatalf("refresh calls = %d, want only the replacement refresh", got)
		}
		manager.mu.RLock()
		pending := len(manager.refreshJobs)
		manager.mu.RUnlock()
		if pending != 0 {
			t.Fatalf("refresh jobs after both jobs ran = %d, want 0", pending)
		}
	})
}
