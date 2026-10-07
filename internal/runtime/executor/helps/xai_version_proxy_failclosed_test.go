package helps

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// An explicitly configured proxy that cannot be used must fail the npm lookup
// instead of falling back to a direct connection.
func TestRefreshXAIClientVersionFailsClosedOnUnusableProxy(t *testing.T) {
	restoreVersion := SetXAIClientVersionForTest(DefaultXAIFallbackClientVersion)
	defer restoreVersion()

	var direct atomic.Int32
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.0.61"}`))
	}))
	defer registry.Close()

	// Reserve a loopback port and close it so the proxy refuses connections.
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatalf("listen: %v", errListen)
	}
	deadProxy := "socks5://" + listener.Addr().String()
	if errClose := listener.Close(); errClose != nil {
		t.Fatalf("close listener: %v", errClose)
	}

	restoreURL := OverrideXAINPMRegistryURLForTest(registry.URL)
	defer restoreURL()

	for _, proxyURL := range []string{deadProxy, "ftp://127.0.0.1:1"} {
		t.Run(proxyURL, func(t *testing.T) {
			restoreProxy := setXAIVersionProxyURLForTest(proxyURL)
			defer restoreProxy()

			if _, errFetch := FetchXAINPMLatestVersion(context.Background(), nil); errFetch == nil {
				t.Fatal("expected the npm lookup to fail through an unusable proxy")
			}
			refreshXAIClientVersion(context.Background())
			if got := GetXAIClientVersion(); got != DefaultXAIFallbackClientVersion {
				t.Fatalf("GetXAIClientVersion() = %q, want fallback %q", got, DefaultXAIFallbackClientVersion)
			}
		})
	}
	if got := direct.Load(); got != 0 {
		t.Fatalf("registry received %d direct requests, want 0", got)
	}
}
