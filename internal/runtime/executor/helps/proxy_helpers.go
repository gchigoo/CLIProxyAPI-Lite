package helps

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

// NewProxyAwareHTTPClient creates an HTTP client with proper proxy configuration priority:
// 1. Use auth.ProxyURL if configured (highest priority)
// 2. Use cfg.ProxyURL if auth proxy is not configured
// 3. Use RoundTripper from context if neither are configured
//
// Parameters:
//   - ctx: The context containing optional RoundTripper
//   - cfg: The application configuration
//   - auth: The authentication information
//   - timeout: The client timeout (0 means no timeout)
//
// Returns:
//   - *http.Client: An HTTP client with configured proxy or transport
func NewProxyAwareHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	httpClient := &http.Client{}
	if timeout > 0 {
		httpClient.Timeout = timeout
	}

	// Priority 1: Use auth.ProxyURL if configured
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}

	// Priority 2: Use cfg.ProxyURL if auth proxy is not configured
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	// If an explicit proxy is configured, it must either be used or fail closed.
	if proxyURL != "" {
		transport, mode, errProxy := buildProxyTransportStrict(proxyURL)
		if errProxy != nil {
			httpClient.Transport = failClosedRoundTripper{err: errProxy}
			return httpClient
		}
		if mode == proxyutil.ModeDirect || mode == proxyutil.ModeProxy {
			httpClient.Transport = transport
			return httpClient
		}
	}

	// Priority 3: Use RoundTripper from context (typically from RoundTripperFor)
	if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
		httpClient.Transport = rt
	}

	return httpClient
}

type failClosedRoundTripper struct {
	err error
}

func (rt failClosedRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, rt.err
}

func newProxyConfigurationError(component string, proxyURL string, err error) error {
	return fmt.Errorf("%s: invalid proxy %s: %w", component, proxyutil.Redact(proxyURL), err)
}

// buildProxyTransport creates an HTTP transport configured for the given proxy URL.
// It supports direct, SOCKS5, HTTP, and HTTPS proxy protocols.
func buildProxyTransport(proxyURL string) *http.Transport {
	transport, mode, errBuild := buildProxyTransportStrict(proxyURL)
	if errBuild != nil || mode == proxyutil.ModeInherit {
		return nil
	}
	return transport
}

func buildProxyTransportStrict(proxyURL string) (*http.Transport, proxyutil.Mode, error) {
	transport, mode, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
	if errBuild != nil {
		return nil, mode, newProxyConfigurationError("proxy", proxyURL, errBuild)
	}
	return transport, mode, nil
}
