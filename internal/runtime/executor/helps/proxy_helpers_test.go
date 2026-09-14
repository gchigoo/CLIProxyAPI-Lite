package helps

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestNewProxyAwareHTTPClientDirectBypassesGlobalProxy(t *testing.T) {
	t.Parallel()

	client := NewProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
		0,
	)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestNewProxyAwareHTTPClientInvalidExplicitProxyFailsClosed(t *testing.T) {
	called := false
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", utlsClientRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	}))
	client := NewProxyAwareHTTPClient(ctx, nil, &cliproxyauth.Auth{ProxyURL: "http://user:secret@"}, 0)

	_, err := client.Get("http://example.com/")
	if err == nil {
		t.Fatal("client.Get() error = nil, want invalid proxy error")
	}
	if called {
		t.Fatal("context RoundTripper was called despite invalid explicit proxy")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "user:secret") {
		t.Fatalf("proxy error leaked credentials: %v", err)
	}
}

func TestBuildProxyTransportStrictAcceptsSupportedExplicitProxies(t *testing.T) {
	t.Parallel()

	for _, proxyURL := range []string{
		"http://user:pass@127.0.0.1:8080",
		"https://user:pass@127.0.0.1:8443",
		"socks5://user:pass@127.0.0.1:1080",
		"socks5h://user:pass@127.0.0.1:1080",
	} {
		t.Run(proxyURL, func(t *testing.T) {
			transport, mode, err := buildProxyTransportStrict(proxyURL)
			if err != nil {
				t.Fatalf("buildProxyTransportStrict() error = %v", err)
			}
			if mode != proxyutil.ModeProxy {
				t.Fatalf("mode = %v, want proxy", mode)
			}
			if transport == nil {
				t.Fatal("transport = nil, want configured transport")
			}
		})
	}
}
