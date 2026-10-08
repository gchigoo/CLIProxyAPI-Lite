package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type staleRefreshFailureExecutor struct {
	schedulerProviderTestExecutor
	started chan struct{}
	release chan struct{}
}

func (e *staleRefreshFailureExecutor) Refresh(_ context.Context, _ *Auth) (*Auth, error) {
	close(e.started)
	<-e.release
	return nil, &Error{HTTPStatus: http.StatusBadRequest, Message: `{"error":"invalid_grant"}`}
}

// A refresh that started from older credentials must not judge credentials
// installed while it was in flight: the stale refresh token's invalid_grant
// says nothing about the new one.
func TestManager_RefreshFailure_DoesNotTerminateConcurrentlyUpdatedCredential(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(newMemoryAuthTestStore(), &RoundRobinSelector{}, nil)
	executor := &staleRefreshFailureExecutor{
		schedulerProviderTestExecutor: schedulerProviderTestExecutor{provider: "antigravity"},
		started:                       make(chan struct{}),
		release:                       make(chan struct{}),
	}
	manager.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "stale-refresh-failure-auth",
		Provider: "antigravity",
		Status:   StatusActive,
		Metadata: map[string]any{
			"access_token":  "access-token-a",
			"refresh_token": "refresh-token-r1",
			"expired":       time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, errRegister := manager.Register(ctx, auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	refreshDone := make(chan error, 1)
	go func() {
		// The request path refreshes after upstream rejected access-token-a.
		_, errRefresh := manager.refreshAuthForRequest(ctx, auth.ID, "access-token-a")
		refreshDone <- errRefresh
	}()
	<-executor.started

	current, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatalf("auth %q not found", auth.ID)
	}
	// A same-epoch update installs a new refresh token and keeps the access token.
	current.Metadata["refresh_token"] = "refresh-token-r2"
	if _, errUpdate := manager.Update(ctx, current); errUpdate != nil {
		t.Fatalf("update concurrent credential: %v", errUpdate)
	}

	close(executor.release)
	if errRefresh := <-refreshDone; errRefresh == nil {
		t.Fatal("stale refresh unexpectedly succeeded")
	}

	updated, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatalf("auth %q not found after refresh", auth.ID)
	}
	if got := updated.Metadata["refresh_token"]; got != "refresh-token-r2" {
		t.Fatalf("refresh_token = %q, want refresh-token-r2", got)
	}
	if hasUnauthorizedAuthFailure(updated) || updated.Status == StatusError || updated.Unavailable {
		t.Fatalf("updated credential was penalized by a stale refresh: status=%s unavailable=%t lastError=%v message=%q",
			updated.Status, updated.Unavailable, updated.LastError, updated.StatusMessage)
	}
}
