package executor

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

// Request-time refreshes run on a context that is never canceled; the optional
// credits lookup they start must still end, or it blocks later lookups forever.
func TestAntigravityPostRefreshCreditsWithoutLifecycleIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var creditsDeadline time.Time
		var hasDeadline bool
		started := time.Now()
		transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://oauth2.googleapis.com/token":
				return issue6199AntigravityJSONResponse(req, `{"access_token":"bounded-access","expires_in":3600}`), nil
			case "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist":
				creditsDeadline, hasDeadline = req.Context().Deadline()
				return issue6199CreditsBalanceResponse(req, 100), nil
			default:
				return nil, fmt.Errorf("unexpected intercepted request: %s", req.URL)
			}
		})
		ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
		executor := &AntigravityExecutor{}
		auth := issue6199AntigravityAuth(t.Name(), t.Name()+"-refresh")
		t.Cleanup(func() {
			antigravityCreditsBalanceByAuth.Delete(auth.ID)
			antigravityCreditsHintRefreshByID.Delete(auth.ID)
		})
		if _, errRefresh := executor.Refresh(ctx, auth.Clone()); errRefresh != nil {
			t.Fatalf("refresh: %v", errRefresh)
		}
		synctest.Wait()
		if !hasDeadline {
			t.Fatal("optional credits lookup without a cancelable lifecycle has no deadline")
		}
		if got := creditsDeadline.Sub(started); got != antigravityCreditsUnboundLifecycleTimeout {
			t.Fatalf("credits deadline = %s after start, want its own %s bound", got, antigravityCreditsUnboundLifecycleTimeout)
		}
	})
}
