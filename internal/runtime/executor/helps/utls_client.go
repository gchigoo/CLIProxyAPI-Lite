package helps

import (
	"bufio"
	"context"
	cryptotls "crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/httpwire"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// utlsRoundTripper implements http.RoundTripper using a Chrome/Node fingerprint.
// After the TLS handshake it honors ALPN: h2 uses HTTP/2, http/1.1 or an omitted
// protocol uses HTTP/1.1. This matches CPA PR #4955 / sub2api PR #5907, which
// exist because forcing HTTP/2 (or letting net/http type-assert *tls.Conn) drops
// ChatGPT/Codex requests onto the wrong protocol.
type utlsRoundTripper struct {
	dialer    proxy.Dialer
	proxyErr  error
	handshake func(ctx context.Context, conn net.Conn, host string) (*tls.UConn, error)
}

const (
	maxHTTP1ResponseHeaderBytes = 10 << 20
	maxInterimHTTP1Responses    = 10
)

var errHTTP1ResponseHeadersTooLarge = errors.New("utls: HTTP/1.1 response headers too large")

type responseHeaderLimitReader struct {
	reader    io.Reader
	remaining int64
}

func (r *responseHeaderLimitReader) Read(p []byte) (int, error) {
	if r.remaining < 0 {
		return r.reader.Read(p)
	}
	if r.remaining == 0 {
		return 0, errHTTP1ResponseHeadersTooLarge
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, errRead := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, errRead
}

func (r *responseHeaderLimitReader) disableLimit() {
	r.remaining = -1
}

type closeConnectionBody struct {
	io.ReadCloser
	closeConnection func() error
	once            sync.Once
	err             error
}

func (b *closeConnectionBody) Close() error {
	if b == nil {
		return nil
	}
	b.once.Do(func() {
		var errConnection error
		if b.closeConnection != nil {
			errConnection = b.closeConnection()
		}
		var errBody error
		if b.ReadCloser != nil {
			errBody = b.ReadCloser.Close()
		}
		b.err = errors.Join(errBody, errConnection)
	})
	return b.err
}

func newUtlsRoundTripper(proxyURL string) *utlsRoundTripper {
	return newUtlsRoundTripperWithHandshake(proxyURL, handshakeChromeAuto)
}

func newGeminiNodeH2RoundTripper(proxyURL string) *utlsRoundTripper {
	sessionCache := tls.NewLRUClientSessionCache(claudeCodeSessionCacheCapacity)
	return newUtlsRoundTripperWithHandshake(proxyURL, func(ctx context.Context, conn net.Conn, host string) (*tls.UConn, error) {
		tlsConn := tls.UClient(conn, newClaudeCodeTLSConfig(host, sessionCache), tls.HelloCustom)
		if errPreset := tlsConn.ApplyPreset(geminiNodeTLSClientHelloSpec()); errPreset != nil {
			return nil, fmt.Errorf("utls: apply Gemini ClientHello: %w", errPreset)
		}
		if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
			return nil, fmt.Errorf("utls: TLS handshake: %w", errHandshake)
		}
		return tlsConn, nil
	})
}

func newUtlsRoundTripperWithHandshake(proxyURL string, handshake func(context.Context, net.Conn, string) (*tls.UConn, error)) *utlsRoundTripper {
	dialer, errProxy := proxyDialerStrictOrDirect(proxyURL)
	return &utlsRoundTripper{dialer: dialer, proxyErr: errProxy, handshake: handshake}
}

func handshakeChromeAuto(ctx context.Context, conn net.Conn, host string) (*tls.UConn, error) {
	tlsConn := tls.UClient(conn, &tls.Config{ServerName: host}, tls.HelloChrome_Auto)
	if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
		return nil, fmt.Errorf("utls: TLS handshake: %w", errHandshake)
	}
	return tlsConn, nil
}

func (t *utlsRoundTripper) createConnection(ctx context.Context, host, addr string) (*tls.UConn, error) {
	if t.proxyErr != nil {
		return nil, t.proxyErr
	}
	if t.dialer == nil {
		return nil, fmt.Errorf("utls: dialer is nil")
	}
	contextDialer, ok := t.dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("utls: dialer does not support context cancellation")
	}
	conn, errDial := contextDialer.DialContext(ctx, "tcp", addr)
	if errDial != nil {
		return nil, fmt.Errorf("utls: dial upstream: %w", errDial)
	}
	handshake := t.handshake
	if handshake == nil {
		handshake = handshakeChromeAuto
	}
	tlsConn, errHandshake := handshake(ctx, conn, host)
	if errHandshake != nil {
		if !errors.Is(errHandshake, context.Canceled) && !errors.Is(errHandshake, context.DeadlineExceeded) {
			_ = conn.Close()
		}
		return nil, errHandshake
	}
	return tlsConn, nil
}

func closeConnectionOnContextCancel(ctx context.Context, conn net.Conn) func() error {
	var (
		once     sync.Once
		errClose error
	)
	closeConnection := func() error {
		once.Do(func() {
			errClose = conn.Close()
		})
		return errClose
	}
	stop := context.AfterFunc(ctx, func() {
		_ = closeConnection()
	})
	return func() error {
		stop()
		return closeConnection()
	}
}

func readFinalHTTP1Response(reader io.Reader, req *http.Request) (*http.Response, error) {
	limitedReader := &responseHeaderLimitReader{reader: reader, remaining: maxHTTP1ResponseHeaderBytes}
	responseReader := bufio.NewReader(limitedReader)
	for interimResponses := 0; ; interimResponses++ {
		resp, errRead := http.ReadResponse(responseReader, req)
		if errRead != nil {
			return nil, errRead
		}
		if resp.StatusCode < http.StatusContinue || resp.StatusCode >= http.StatusOK || resp.StatusCode == http.StatusSwitchingProtocols {
			limitedReader.disableLimit()
			return resp, nil
		}
		if interimResponses == maxInterimHTTP1Responses {
			return nil, fmt.Errorf("utls: too many interim HTTP/1.1 responses")
		}
	}
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	hostname := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(hostname, port)

	tlsConn, err := t.createConnection(req.Context(), hostname, addr)
	if err != nil {
		return nil, err
	}

	var (
		resp            *http.Response
		closeConnection func() error
	)
	switch tlsConn.ConnectionState().NegotiatedProtocol {
	case "h2":
		// This is intentionally single-use until a pooled http2.Transport can keep
		// ALPN checks and HTTP/1.1 fallback semantics under test.
		tr := &http2.Transport{}
		h2Conn, errClientConn := tr.NewClientConn(tlsConn)
		if errClientConn != nil {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("utls: initialize HTTP/2 connection: %w", errClientConn)
		}
		resp, err = h2Conn.RoundTrip(req)
		closeConnection = h2Conn.Close
	case "", "http/1.1":
		closeConnection = closeConnectionOnContextCancel(req.Context(), tlsConn)
		if errWrite := req.Write(tlsConn); errWrite != nil {
			_ = closeConnection()
			if errContext := req.Context().Err(); errContext != nil {
				return nil, errContext
			}
			return nil, fmt.Errorf("utls: write HTTP/1.1 request: %w", errWrite)
		}
		resp, err = readFinalHTTP1Response(tlsConn, req)
	default:
		negotiated := tlsConn.ConnectionState().NegotiatedProtocol
		_ = tlsConn.Close()
		return nil, fmt.Errorf("utls: unsupported negotiated protocol %q", negotiated)
	}
	if err != nil {
		if closeConnection != nil {
			_ = closeConnection()
		}
		if errContext := req.Context().Err(); errContext != nil {
			return nil, errContext
		}
		return nil, err
	}
	if resp == nil {
		if closeConnection != nil {
			_ = closeConnection()
		}
		return nil, fmt.Errorf("utls: upstream returned an empty response")
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	resp.Body = &closeConnectionBody{
		ReadCloser:      resp.Body,
		closeConnection: closeConnection,
	}
	return resp, nil
}

// claudeCodeSessionCacheCapacity bounds the per-transport TLS session cache for
// the Anthropic inference plane.
const claudeCodeSessionCacheCapacity = 32

// newClaudeCodeTLSConfig builds the uTLS config for one inference-plane dial.
//
// OmitEmptyPsk keeps the pre_shared_key extension silent until a session is
// cached, so an unresumed ClientHello stays byte-identical to the captured
// native handshake. PreferSkipResumptionOnNilExtension turns uTLS's HelloCustom
// "resume without the matching extension" panic into a skipped resumption.
func newClaudeCodeTLSConfig(host string, sessionCache tls.ClientSessionCache) *tls.Config {
	return &tls.Config{
		ServerName:                         host,
		ClientSessionCache:                 sessionCache,
		OmitEmptyPsk:                       true,
		PreferSkipResumptionOnNilExtension: true,
	}
}

// claudeCodeTLSClientHelloSpec reproduces the deterministic Node/OpenSSL
// ClientHello emitted by Claude Code 2.1.220 on macOS arm64. Keep this spec in
// sync with a fresh native capture whenever the advertised Claude Code version
// changes.
func claudeCodeTLSClientHelloSpec() *tls.ClientHelloSpec {
	return &tls.ClientHelloSpec{
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
		CompressionMethods: []uint8{0},
		Extensions: []tls.TLSExtension{
			&tls.SNIExtension{},
			&tls.ExtendedMasterSecretExtension{},
			&tls.RenegotiationInfoExtension{Renegotiation: tls.RenegotiateOnceAsClient},
			&tls.SupportedCurvesExtension{Curves: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}},
			&tls.SupportedPointsExtension{SupportedPoints: []byte{0}},
			&tls.SessionTicketExtension{},
			&tls.ALPNExtension{AlpnProtocols: []string{"http/1.1"}},
			&tls.StatusRequestExtension{},
			&tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []tls.SignatureScheme{
				tls.ECDSAWithP256AndSHA256,
				tls.PSSWithSHA256,
				tls.PKCS1WithSHA256,
				tls.ECDSAWithP384AndSHA384,
				tls.PSSWithSHA384,
				tls.PKCS1WithSHA384,
				tls.PSSWithSHA512,
				tls.PKCS1WithSHA512,
				tls.PKCS1WithSHA1,
			}},
			&tls.SCTExtension{},
			&tls.KeyShareExtension{KeyShares: []tls.KeyShare{{Group: tls.X25519}}},
			&tls.PSKKeyExchangeModesExtension{Modes: []uint8{tls.PskModeDHE}},
			&tls.SupportedVersionsExtension{Versions: []uint16{tls.VersionTLS13, tls.VersionTLS12}},
			&tls.UtlsPaddingExtension{GetPaddingLen: tls.BoringPaddingStyle},
			// pre_shared_key MUST be the final extension (RFC 8446 4.2.11), after
			// padding. It contributes zero bytes until a cached session exists.
			&tls.UtlsPreSharedKeyExtension{},
		},
	}
}

func geminiNodeTLSClientHelloSpec() *tls.ClientHelloSpec {
	spec := claudeCodeTLSClientHelloSpec()
	for i, ext := range spec.Extensions {
		if alpn, ok := ext.(*tls.ALPNExtension); ok {
			copied := *alpn
			copied.AlpnProtocols = []string{"h2", "http/1.1"}
			spec.Extensions[i] = &copied
			break
		}
	}
	return spec
}

func antigravityNodeTLSClientHelloSpec() *tls.ClientHelloSpec {
	spec := claudeCodeTLSClientHelloSpec()
	filtered := make([]tls.TLSExtension, 0, len(spec.Extensions))
	for _, ext := range spec.Extensions {
		if _, ok := ext.(*tls.ALPNExtension); ok {
			continue
		}
		filtered = append(filtered, ext)
	}
	spec.Extensions = filtered
	return spec
}

const claudeCodeRoundTripperCacheCapacity = 64

var claudeCodeRoundTripperCache = internalcache.NewBoundedLRU[string, http.RoundTripper](
	claudeCodeRoundTripperCacheCapacity,
	func(_ string, roundTripper http.RoundTripper) {
		if transport, ok := roundTripper.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
	},
)

var claudeCodeMessagesHeaderOrder = []string{
	"Accept",
	"Authorization",
	"Content-Type",
	"User-Agent",
	"X-Claude-Code-Session-Id",
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-OS",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
	"anthropic-beta",
	"anthropic-dangerous-direct-browser-access",
	"anthropic-version",
	"x-app",
	"x-client-request-id",
	"Connection",
	"Host",
	"Accept-Encoding",
	"Content-Length",
}

var claudeCodeCountTokensHeaderOrder = []string{
	"Accept",
	"Authorization",
	"Content-Type",
	"User-Agent",
	"X-Claude-Code-Session-Id",
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-OS",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"anthropic-beta",
	"anthropic-dangerous-direct-browser-access",
	"anthropic-version",
	"x-app",
	"x-client-request-id",
	"Connection",
	"Host",
	"Accept-Encoding",
	"Content-Length",
}

func claudeCodeRequestHeaderOrder(_, requestTarget string) []string {
	if strings.HasPrefix(requestTarget, "/v1/messages/count_tokens") {
		return claudeCodeCountTokensHeaderOrder
	}
	return claudeCodeMessagesHeaderOrder
}

func cachedClaudeCodeRoundTripper(proxyURL string) http.RoundTripper {
	return claudeCodeRoundTripperCache.GetOrAdd(proxyURL, func() http.RoundTripper {
		return newClaudeCodeRoundTripper(proxyURL)
	})
}

func newClaudeCodeRoundTripper(proxyURL string) http.RoundTripper {
	// The cache is scoped to this round tripper, which is already keyed by proxy,
	// so resumption never crosses proxy boundaries.
	sessionCache := tls.NewLRUClientSessionCache(claudeCodeSessionCacheCapacity)
	dialer, errProxy := proxyDialerStrictOrDirect(proxyURL)
	if errProxy != nil {
		return failClosedRoundTripper{err: errProxy}
	}

	transport := &http.Transport{
		ForceAttemptHTTP2: false,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var (
				conn net.Conn
				err  error
			)
			if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
				conn, err = contextDialer.DialContext(ctx, network, addr)
			} else {
				conn, err = dialer.Dial(network, addr)
			}
			if err != nil {
				return nil, fmt.Errorf("claude tls: dial upstream: %w", err)
			}

			host, _, errSplit := net.SplitHostPort(addr)
			if errSplit != nil {
				if errClose := conn.Close(); errClose != nil {
					log.Debugf("claude tls: close failed connection: %v", errClose)
				}
				return nil, fmt.Errorf("claude tls: split upstream address: %w", errSplit)
			}
			tlsConn := tls.UClient(conn, newClaudeCodeTLSConfig(host, sessionCache), tls.HelloCustom)
			if errPreset := tlsConn.ApplyPreset(claudeCodeTLSClientHelloSpec()); errPreset != nil {
				if errClose := tlsConn.Close(); errClose != nil {
					log.Debugf("claude tls: close connection after preset failure: %v", errClose)
				}
				return nil, fmt.Errorf("claude tls: apply Claude Code ClientHello: %w", errPreset)
			}
			if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
				if errClose := tlsConn.Close(); errClose != nil {
					log.Debugf("claude tls: close connection after handshake failure: %v", errClose)
				}
				return nil, fmt.Errorf("claude tls: handshake upstream: %w", errHandshake)
			}
			return httpwire.NewOrderedRequestConn(tlsConn, claudeCodeRequestHeaderOrder), nil
		},
	}
	return transport
}

// providerTLSRoundTripperCacheCapacity bounds cached provider TLS dial policies.
// ChatGPT and Gemini entries are not connection pools; utlsRoundTripper creates
// a single-use connection and closes it when the response body is closed.
const providerTLSRoundTripperCacheCapacity = 64

var (
	chatGPTRoundTripperCache = internalcache.NewBoundedLRU[string, http.RoundTripper](
		providerTLSRoundTripperCacheCapacity,
		closeCachedRoundTripper,
	)
	geminiNodeRoundTripperCache = internalcache.NewBoundedLRU[string, http.RoundTripper](
		providerTLSRoundTripperCacheCapacity,
		closeCachedRoundTripper,
	)
	cloudCodeRoundTripperCache = internalcache.NewBoundedLRU[string, http.RoundTripper](
		providerTLSRoundTripperCacheCapacity,
		closeCachedRoundTripper,
	)
)

func closeCachedRoundTripper(_ string, roundTripper http.RoundTripper) {
	if transport, ok := roundTripper.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func cachedChatGPTRoundTripper(proxyURL string) http.RoundTripper {
	return chatGPTRoundTripperCache.GetOrAdd(proxyURL, func() http.RoundTripper {
		return newUtlsRoundTripper(proxyURL)
	})
}

func cachedGeminiNodeRoundTripper(proxyURL string) http.RoundTripper {
	return geminiNodeRoundTripperCache.GetOrAdd(proxyURL, func() http.RoundTripper {
		return newGeminiNodeH2RoundTripper(proxyURL)
	})
}

func cachedCloudCodeRoundTripper(proxyURL string) http.RoundTripper {
	return cloudCodeRoundTripperCache.GetOrAdd(proxyURL, func() http.RoundTripper {
		return newNodeTLSRoundTripper(proxyURL, false, antigravityNodeTLSClientHelloSpec)
	})
}

func proxyDialerStrictOrDirect(proxyURL string) (proxy.Dialer, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return proxy.Direct, nil
	}
	proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
	if errBuild != nil {
		return nil, newProxyConfigurationError("utls", proxyURL, errBuild)
	}
	switch mode {
	case proxyutil.ModeInherit, proxyutil.ModeDirect:
		return proxy.Direct, nil
	case proxyutil.ModeProxy:
		if proxyDialer == nil {
			return nil, newProxyConfigurationError("utls", proxyURL, fmt.Errorf("proxy dialer is nil"))
		}
		return proxyDialer, nil
	default:
		return nil, newProxyConfigurationError("utls", proxyURL, fmt.Errorf("unsupported proxy mode %d", mode))
	}
}

func newNodeTLSRoundTripper(proxyURL string, forceHTTP2 bool, specFn func() *tls.ClientHelloSpec) http.RoundTripper {
	sessionCache := tls.NewLRUClientSessionCache(claudeCodeSessionCacheCapacity)
	dialer, errProxy := proxyDialerStrictOrDirect(proxyURL)
	if errProxy != nil {
		return failClosedRoundTripper{err: errProxy}
	}
	transport := &http.Transport{
		ForceAttemptHTTP2:     forceHTTP2,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       10 * time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, host, errDial := dialTLSBaseConn(ctx, dialer, network, addr)
			if errDial != nil {
				return nil, errDial
			}
			tlsConn := tls.UClient(conn, newClaudeCodeTLSConfig(host, sessionCache), tls.HelloCustom)
			if errPreset := tlsConn.ApplyPreset(specFn()); errPreset != nil {
				if errClose := tlsConn.Close(); errClose != nil {
					log.Debugf("node tls: close connection after preset failure: %v", errClose)
				}
				return nil, fmt.Errorf("node tls: apply ClientHello: %w", errPreset)
			}
			if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
				if errClose := tlsConn.Close(); errClose != nil {
					log.Debugf("node tls: close connection after handshake failure: %v", errClose)
				}
				return nil, fmt.Errorf("node tls: handshake upstream: %w", errHandshake)
			}
			return tlsConn, nil
		},
	}
	return transport
}

func dialTLSBaseConn(ctx context.Context, dialer proxy.Dialer, network, addr string) (net.Conn, string, error) {
	var (
		conn net.Conn
		err  error
	)
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		conn, err = contextDialer.DialContext(ctx, network, addr)
	} else {
		conn, err = dialer.Dial(network, addr)
	}
	if err != nil {
		return nil, "", fmt.Errorf("utls: dial upstream: %w", err)
	}
	host, _, errSplit := net.SplitHostPort(addr)
	if errSplit != nil {
		if errClose := conn.Close(); errClose != nil {
			log.Debugf("utls: close failed connection: %v", errClose)
		}
		return nil, "", fmt.Errorf("utls: split upstream address: %w", errSplit)
	}
	return conn, host, nil
}

func antigravityTLSConfigForConn(base *cryptotls.Config, host string, sessionCache tls.ClientSessionCache) *tls.Config {
	cfg := newClaudeCodeTLSConfig(host, sessionCache)
	if base != nil {
		cfg = cloneStdTLSConfigForUTLS(base)
		if cfg.ServerName == "" {
			cfg.ServerName = host
		}
		cfg.ClientSessionCache = sessionCache
		cfg.OmitEmptyPsk = true
		cfg.PreferSkipResumptionOnNilExtension = true
	}
	cfg.NextProtos = nil
	cfg.ApplicationSettings = nil
	return cfg
}

func cloneStdTLSConfigForUTLS(base *cryptotls.Config) *tls.Config {
	if base == nil {
		return &tls.Config{}
	}
	cloned := base.Clone()
	return &tls.Config{
		Rand:                                cloned.Rand,
		Time:                                cloned.Time,
		Certificates:                        stdCertificatesToUTLS(cloned.Certificates),
		GetClientCertificate:                stdGetClientCertificateToUTLS(cloned.GetClientCertificate),
		VerifyPeerCertificate:               cloned.VerifyPeerCertificate,
		VerifyConnection:                    stdVerifyConnectionToUTLS(cloned.VerifyConnection),
		RootCAs:                             cloned.RootCAs,
		NextProtos:                          append([]string(nil), cloned.NextProtos...),
		ServerName:                          cloned.ServerName,
		ClientAuth:                          tls.ClientAuthType(cloned.ClientAuth),
		ClientCAs:                           cloned.ClientCAs,
		InsecureSkipVerify:                  cloned.InsecureSkipVerify,
		CipherSuites:                        append([]uint16(nil), cloned.CipherSuites...),
		PreferServerCipherSuites:            cloned.PreferServerCipherSuites,
		SessionTicketsDisabled:              cloned.SessionTicketsDisabled,
		SessionTicketKey:                    cloned.SessionTicketKey,
		MinVersion:                          cloned.MinVersion,
		MaxVersion:                          cloned.MaxVersion,
		CurvePreferences:                    stdCurveIDsToUTLS(cloned.CurvePreferences),
		DynamicRecordSizingDisabled:         cloned.DynamicRecordSizingDisabled,
		Renegotiation:                       tls.RenegotiationSupport(cloned.Renegotiation),
		KeyLogWriter:                        cloned.KeyLogWriter,
		EncryptedClientHelloConfigList:      append([]byte(nil), cloned.EncryptedClientHelloConfigList...),
		EncryptedClientHelloRejectionVerify: stdECHRejectionVerifyToUTLS(cloned.EncryptedClientHelloRejectionVerify),
	}
}

func stdCertificatesToUTLS(certs []cryptotls.Certificate) []tls.Certificate {
	if len(certs) == 0 {
		return nil
	}
	out := make([]tls.Certificate, len(certs))
	for i := range certs {
		out[i] = stdCertificateToUTLS(certs[i])
	}
	return out
}

func stdCertificateToUTLS(cert cryptotls.Certificate) tls.Certificate {
	return tls.Certificate{
		Certificate:                  cert.Certificate,
		PrivateKey:                   cert.PrivateKey,
		SupportedSignatureAlgorithms: stdSignatureSchemesToUTLS(cert.SupportedSignatureAlgorithms),
		OCSPStaple:                   cert.OCSPStaple,
		SignedCertificateTimestamps:  cert.SignedCertificateTimestamps,
		Leaf:                         cert.Leaf,
	}
}

func stdCertificatePtrToUTLS(cert *cryptotls.Certificate) *tls.Certificate {
	if cert == nil {
		return nil
	}
	converted := stdCertificateToUTLS(*cert)
	return &converted
}

func stdGetClientCertificateToUTLS(fn func(*cryptotls.CertificateRequestInfo) (*cryptotls.Certificate, error)) func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	if fn == nil {
		return nil
	}
	return func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		stdInfo := &cryptotls.CertificateRequestInfo{}
		if info != nil {
			stdInfo.AcceptableCAs = info.AcceptableCAs
			stdInfo.SignatureSchemes = utlsSignatureSchemesToStd(info.SignatureSchemes)
			stdInfo.Version = info.Version
		}
		cert, err := fn(stdInfo)
		return stdCertificatePtrToUTLS(cert), err
	}
}

func stdVerifyConnectionToUTLS(fn func(cryptotls.ConnectionState) error) func(tls.ConnectionState) error {
	if fn == nil {
		return nil
	}
	return func(state tls.ConnectionState) error {
		return fn(utlsConnectionStateToStd(state))
	}
}

func stdECHRejectionVerifyToUTLS(fn func(cryptotls.ConnectionState) error) func(tls.ConnectionState) error {
	if fn == nil {
		return nil
	}
	return func(state tls.ConnectionState) error {
		return fn(utlsConnectionStateToStd(state))
	}
}

func utlsConnectionStateToStd(state tls.ConnectionState) cryptotls.ConnectionState {
	return cryptotls.ConnectionState{
		Version:                     state.Version,
		HandshakeComplete:           state.HandshakeComplete,
		DidResume:                   state.DidResume,
		CipherSuite:                 state.CipherSuite,
		NegotiatedProtocol:          state.NegotiatedProtocol,
		NegotiatedProtocolIsMutual:  state.NegotiatedProtocolIsMutual,
		ServerName:                  state.ServerName,
		PeerCertificates:            state.PeerCertificates,
		VerifiedChains:              state.VerifiedChains,
		SignedCertificateTimestamps: state.SignedCertificateTimestamps,
		OCSPResponse:                state.OCSPResponse,
		TLSUnique:                   state.TLSUnique,
		ECHAccepted:                 state.ECHAccepted,
	}
}

func stdSignatureSchemesToUTLS(schemes []cryptotls.SignatureScheme) []tls.SignatureScheme {
	if len(schemes) == 0 {
		return nil
	}
	out := make([]tls.SignatureScheme, len(schemes))
	for i, scheme := range schemes {
		out[i] = tls.SignatureScheme(scheme)
	}
	return out
}

func utlsSignatureSchemesToStd(schemes []tls.SignatureScheme) []cryptotls.SignatureScheme {
	if len(schemes) == 0 {
		return nil
	}
	out := make([]cryptotls.SignatureScheme, len(schemes))
	for i, scheme := range schemes {
		out[i] = cryptotls.SignatureScheme(scheme)
	}
	return out
}

func stdCurveIDsToUTLS(curves []cryptotls.CurveID) []tls.CurveID {
	if len(curves) == 0 {
		return nil
	}
	out := make([]tls.CurveID, len(curves))
	for i, curve := range curves {
		out[i] = tls.CurveID(curve)
	}
	return out
}

// InstallAntigravityNodeTLS replaces a transport's TLS dialer with the native
// Antigravity Node/OpenSSL ClientHello (HTTP/1.1, no ALPN).
func InstallAntigravityNodeTLS(transport *http.Transport) {
	if transport == nil {
		return
	}
	sessionCache := tls.NewLRUClientSessionCache(claudeCodeSessionCacheCapacity)
	baseDial := transport.DialContext
	if baseDial == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		baseDial = dialer.DialContext
	}
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, errDial := baseDial(ctx, network, addr)
		if errDial != nil {
			return nil, fmt.Errorf("antigravity tls: dial upstream: %w", errDial)
		}
		host, _, errSplit := net.SplitHostPort(addr)
		if errSplit != nil {
			if errClose := conn.Close(); errClose != nil {
				log.Debugf("antigravity tls: close failed connection: %v", errClose)
			}
			return nil, fmt.Errorf("antigravity tls: split upstream address: %w", errSplit)
		}
		cfg := antigravityTLSConfigForConn(transport.TLSClientConfig, host, sessionCache)
		tlsConn := tls.UClient(conn, cfg, tls.HelloCustom)
		if errPreset := tlsConn.ApplyPreset(antigravityNodeTLSClientHelloSpec()); errPreset != nil {
			if errClose := tlsConn.Close(); errClose != nil {
				log.Debugf("antigravity tls: close connection after preset failure: %v", errClose)
			}
			return nil, fmt.Errorf("antigravity tls: apply ClientHello: %w", errPreset)
		}
		if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
			if errClose := tlsConn.Close(); errClose != nil {
				log.Debugf("antigravity tls: close connection after handshake failure: %v", errClose)
			}
			return nil, fmt.Errorf("antigravity tls: handshake upstream: %w", errHandshake)
		}
		return tlsConn, nil
	}
}

// fallbackRoundTripper uses provider-specific TLS fingerprints for protected
// HTTPS hosts and falls back to the standard transport for all other requests.
type fallbackRoundTripper struct {
	anthropic http.RoundTripper
	chrome    http.RoundTripper
	googleH2  http.RoundTripper
	googleH1  http.RoundTripper
	fallback  http.RoundTripper
}

func (f *fallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && IsAnthropicUpstreamURL(req.URL) {
		return f.anthropic.RoundTrip(req)
	}
	if isChatGPTUpstreamRequest(req) {
		return f.chrome.RoundTrip(req)
	}
	if isCloudCodeUpstreamRequest(req) && f.googleH1 != nil {
		return f.googleH1.RoundTrip(req)
	}
	if isGoogleInferenceUpstreamRequest(req) && f.googleH2 != nil {
		return f.googleH2.RoundTrip(req)
	}
	return f.fallback.RoundTrip(req)
}

func isChatGPTUpstreamRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
		return false
	}
	host := strings.ToLower(req.URL.Hostname())
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com")
}

func isGoogleInferenceUpstreamRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
		return false
	}
	host := strings.ToLower(req.URL.Hostname())
	return host == "generativelanguage.googleapis.com" || host == "aiplatform.googleapis.com" || strings.HasSuffix(host, "-aiplatform.googleapis.com")
}

func isCloudCodeUpstreamRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
		return false
	}
	host := strings.ToLower(req.URL.Hostname())
	return host == "cloudcode-pa.googleapis.com" || strings.HasSuffix(host, "-cloudcode-pa.googleapis.com")
}

// NewUtlsHTTPClient creates an HTTP client using provider-specific TLS
// fingerprints for protected hosts. It uses Claude Code's Node/OpenSSL profile
// for Anthropic, a one-shot Chrome profile for ChatGPT, a one-shot Node/OpenSSL
// HTTP/2 profile for Gemini and Vertex inference APIs, and a pooled Node
// HTTP/1.1 profile for Cloud Code / Antigravity, with a standard-transport
// fallback for other hosts.
func NewUtlsHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	var ctxRoundTripper http.RoundTripper
	if ctx != nil {
		ctxRoundTripper, _ = ctx.Value("cliproxy.roundtripper").(http.RoundTripper)
	}

	var standardTransport http.RoundTripper = http.DefaultTransport
	if proxyURL != "" {
		transport, mode, errProxy := buildProxyTransportStrict(proxyURL)
		if errProxy != nil {
			return &http.Client{Transport: failClosedRoundTripper{err: errProxy}, Timeout: timeout}
		}
		if mode == proxyutil.ModeDirect || mode == proxyutil.ModeProxy {
			standardTransport = transport
		}
	}

	var chromeRT http.RoundTripper = cachedChatGPTRoundTripper(proxyURL)
	var anthropicRT http.RoundTripper = cachedClaudeCodeRoundTripper(proxyURL)
	var googleH2RT http.RoundTripper = cachedGeminiNodeRoundTripper(proxyURL)
	var googleH1RT http.RoundTripper = cachedCloudCodeRoundTripper(proxyURL)
	if proxyURL == "" && ctxRoundTripper != nil {
		chromeRT = ctxRoundTripper
		anthropicRT = ctxRoundTripper
		googleH2RT = ctxRoundTripper
		googleH1RT = ctxRoundTripper
		standardTransport = ctxRoundTripper
	}

	client := &http.Client{
		Transport: &fallbackRoundTripper{
			anthropic: anthropicRT,
			chrome:    chromeRT,
			googleH2:  googleH2RT,
			googleH1:  googleH1RT,
			fallback:  standardTransport,
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

// NewUTLSWebsocketDialContext returns a Chrome uTLS dialer that advertises
// HTTP/1.1 only. Official Codex WebSockets upgrade over HTTP/1.1; offering h2
// or ALPS is a detectable mismatch (CPA PR #4929).
func NewUTLSWebsocketDialContext(proxyURL string, baseConfigs ...*cryptotls.Config) (func(context.Context, string, string) (net.Conn, error), error) {
	var base *cryptotls.Config
	if len(baseConfigs) > 0 {
		base = baseConfigs[0]
	}
	return newUTLSWebsocketDialContext(proxyURL, base)
}

func newUTLSWebsocketDialContext(proxyURL string, base *cryptotls.Config) (func(context.Context, string, string) (net.Conn, error), error) {
	dialer, errProxy := proxyDialerStrictOrDirect(proxyURL)
	if errProxy != nil {
		return nil, errProxy
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("utls websocket: dialer does not support context cancellation")
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		rawConn, errDial := contextDialer.DialContext(ctx, network, addr)
		if errDial != nil {
			return nil, fmt.Errorf("utls websocket: dial upstream: %w", errDial)
		}
		host, _, errSplit := net.SplitHostPort(addr)
		if errSplit != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("utls websocket: split upstream address: %w", errSplit)
		}
		configForConn := &tls.Config{ServerName: host}
		if base != nil {
			configForConn = cloneStdTLSConfigForUTLS(base)
			if configForConn.ServerName == "" {
				configForConn.ServerName = host
			}
		}
		configForConn.NextProtos = []string{"http/1.1"}
		configForConn.ApplicationSettings = nil
		spec, errSpec := codexChromeWebsocketClientHelloSpec()
		if errSpec != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("build Chrome websocket ClientHello: %w", errSpec)
		}
		tlsConn := tls.UClient(rawConn, configForConn, tls.HelloCustom)
		if errPreset := tlsConn.ApplyPreset(&spec); errPreset != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("apply Chrome websocket ClientHello: %w", errPreset)
		}
		if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("Chrome websocket TLS handshake: %w", errHandshake)
		}
		return tlsConn, nil
	}, nil
}

func codexChromeWebsocketClientHelloSpec() (tls.ClientHelloSpec, error) {
	spec, errSpec := tls.UTLSIdToSpec(tls.HelloChrome_Auto)
	if errSpec != nil {
		return tls.ClientHelloSpec{}, errSpec
	}
	extensions := make([]tls.TLSExtension, 0, len(spec.Extensions))
	for _, extension := range spec.Extensions {
		switch typed := extension.(type) {
		case *tls.ALPNExtension:
			typed.AlpnProtocols = []string{"http/1.1"}
			extensions = append(extensions, typed)
		case *tls.ApplicationSettingsExtension, *tls.ApplicationSettingsExtensionNew:
			continue
		default:
			extensions = append(extensions, extension)
		}
	}
	spec.Extensions = extensions
	return spec, nil
}
