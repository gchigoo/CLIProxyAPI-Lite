package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

// Lite honors an executor retry-after on 404 and model-support failures only
// for Meta credentials; other providers keep the fixed 12 hour cooldown.
func TestMetaRetryAfterGatesNotFoundAndModelSupportCooldown(t *testing.T) {
	previous := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(previous) })

	const modelSupportMessage = "requested model is not supported"
	retryAfter := 5 * time.Minute
	tests := []struct {
		name      string
		provider  string
		status    int
		message   string
		wantShort bool
	}{
		{name: "meta 404", provider: "meta", status: http.StatusNotFound, message: "not found", wantShort: true},
		{name: "meta model support", provider: "meta", status: http.StatusBadRequest, message: modelSupportMessage, wantShort: true},
		{name: "claude 404", provider: "claude", status: http.StatusNotFound, message: "not found"},
		{name: "antigravity model support", provider: "antigravity", status: http.StatusBadRequest, message: modelSupportMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(nil, nil, nil)
			auth := &Auth{ID: "retry-after-" + tc.provider + "-" + tc.name, Provider: tc.provider}
			reg := registry.GetGlobalRegistry()
			reg.RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "m", Created: time.Now().Unix()}})
			t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
			if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
				t.Fatalf("register auth: %v", errRegister)
			}

			before := time.Now()
			m.MarkResult(context.Background(), Result{
				AuthID: auth.ID, Provider: auth.Provider, Model: "m", Success: false,
				RetryAfter: &retryAfter,
				Error:      &Error{HTTPStatus: tc.status, Message: tc.message},
			})
			updated, ok := m.GetByID(auth.ID)
			if !ok || updated == nil || updated.ModelStates["m"] == nil {
				t.Fatalf("model state missing after failure: %#v", updated)
			}
			assertRetryAfterCooldown(t, "model", updated.ModelStates["m"].NextRetryAfter.Sub(before), tc.wantShort)

			if tc.status != http.StatusNotFound {
				return
			}
			credential := &Auth{Provider: tc.provider}
			now := time.Now()
			applyAuthFailureState(credential, &Error{HTTPStatus: tc.status, Message: tc.message}, &retryAfter, now, false)
			assertRetryAfterCooldown(t, "credential", credential.NextRetryAfter.Sub(now), tc.wantShort)
		})
	}
}

func assertRetryAfterCooldown(t *testing.T, scope string, remaining time.Duration, wantShort bool) {
	t.Helper()
	if wantShort {
		if remaining < 4*time.Minute || remaining > 6*time.Minute {
			t.Fatalf("%s cooldown = %v, want the 5m retry-after", scope, remaining)
		}
		return
	}
	if remaining < 11*time.Hour {
		t.Fatalf("%s cooldown = %v, want the 12h default", scope, remaining)
	}
}
