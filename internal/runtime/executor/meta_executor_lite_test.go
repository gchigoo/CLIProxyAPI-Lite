package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// An invalid explicit proxy must fail the request-time mint instead of
// sending the DCA token over a direct connection.
func TestMetaExecutorRefreshFailsClosedOnInvalidProxy(t *testing.T) {
	var mintCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mintCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_key":"LLM|direct","base_url":"https://api.meta.ai/v1"}`))
	}))
	defer server.Close()
	t.Setenv("META_MINT_URL", server.URL)

	auth := &cliproxyauth.Auth{
		ID:       "meta-proxy.json",
		Provider: "meta",
		ProxyURL: "socks5://%zz-invalid",
		Metadata: map[string]any{"type": "meta", "dca_token": "dca:secret"},
	}
	_, err := NewMetaExecutor(&config.Config{}).Refresh(context.Background(), auth)
	if err == nil {
		t.Fatal("Refresh() error = nil, want proxy error")
	}
	if got := mintCalls.Load(); got != 0 {
		t.Fatalf("mint endpoint reached directly %d times with an invalid explicit proxy", got)
	}
	if strings.Contains(err.Error(), "dca:secret") {
		t.Fatalf("error leaks the DCA token: %v", err)
	}
}
