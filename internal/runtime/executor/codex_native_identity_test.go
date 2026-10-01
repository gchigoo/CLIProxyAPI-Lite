package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

const (
	codexNativeResponsesURL   = "https://example.com/responses"
	codexNativeTestCredential = "codex-test-caller"
)

func TestCodexNativeIdentityAstraUsesCurrentClientVersion(t *testing.T) {
	const expectedVersion = "0.155.0"
	if codexCLIVersion != expectedVersion || codexNativeVersion != expectedVersion {
		t.Fatalf("identity versions = %q/%q, want %q", codexCLIVersion, codexNativeVersion, expectedVersion)
	}
	executor := newCodexNativeTestExecutor()
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	req.Model = "gpt-6-astra"
	req.Payload = []byte(`{"model":"gpt-6-astra","stream":true,"input":[{"role":"user","content":"hello"}]}`)
	ctx := contextWithCodexDownstreamHeaders(map[string]string{
		"User-Agent": "codex_exec/0.155.0",
		"Version":    "0.149.0",
	})
	httpReq, body, _, state, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, req.Payload)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if !state.enabled || gjson.GetBytes(body, "model").String() != "gpt-6-astra" {
		t.Fatal("Astra request did not retain its model and native identity")
	}
	if got := gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String(); got != state.installationID || got == "" {
		t.Fatalf("installation ID = %q, want %q", got, state.installationID)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg)
	applyCodexNativeIdentityHeaders(httpReq.Header, &state)
	websocketHeaders := make(http.Header)
	applyCodexNativeIdentityWebsocketHeaders(websocketHeaders, &state)
	for transport, headers := range map[string]http.Header{"http": httpReq.Header, "websocket": websocketHeaders} {
		if got := headers.Get("Version"); got != expectedVersion {
			t.Fatalf("%s version = %q, want %q", transport, got, expectedVersion)
		}
		if got := headers.Get("User-Agent"); !strings.HasPrefix(got, "codex_cli_rs/"+expectedVersion+" ") || !strings.Contains(got, "(codex-tui; "+expectedVersion+")") {
			t.Fatalf("%s user-agent = %q, want consistent current identity", transport, got)
		}
		if got := headers.Get("Originator"); got != "codex_cli_rs" {
			t.Fatalf("%s originator = %q, want preserved native originator", transport, got)
		}
	}
}

func TestCodexNativeIdentitySynthesizesMatchingBodyAndHeaders(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	executor := newCodexNativeTestExecutor()
	ctx := contextWithCodexDownstreamHeaders(map[string]string{
		"User-Agent": "codex-pager/1.0.3 codex-shell/1.0.3 (macos; aarch64)",
	})
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	rawJSON := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"role":"user","content":"hello"}]}`)

	httpReq, body, _, nativeState, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if !nativeState.enabled {
		t.Fatal("native identity state was not enabled for Responses OAuth request")
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg)
	applyCodexNativeIdentityHeaders(httpReq.Header, &nativeState)

	assertCodexNativeUUID(t, "installation ID", nativeState.installationID)
	assertCodexNativeUUID(t, "session ID", nativeState.sessionID)
	assertCodexNativeUUID(t, "thread ID", nativeState.threadID)
	assertCodexNativeUUID(t, "turn ID", nativeState.turnID)
	if nativeState.windowID != nativeState.threadID+":0" {
		t.Fatalf("window ID = %q, want %q", nativeState.windowID, nativeState.threadID+":0")
	}

	for path, want := range map[string]string{
		"prompt_cache_key":                        nativeState.sessionID,
		"client_metadata.x-codex-installation-id": nativeState.installationID,
		"client_metadata.session_id":              nativeState.sessionID,
		"client_metadata.thread_id":               nativeState.threadID,
		"client_metadata.turn_id":                 nativeState.turnID,
		"client_metadata.x-codex-window-id":       nativeState.windowID,
		"client_metadata.x-codex-turn-metadata":   nativeState.turnMetadata,
	} {
		if got := gjson.GetBytes(body, path).String(); got != want {
			t.Fatalf("body %s = %q, want %q; body=%s", path, got, want, string(body))
		}
	}
	assertCodexNativeTurnMetadata(t, nativeState.turnMetadata, nativeState)
	assertCodexNativeHeaders(t, httpReq.Header, nativeState)
	if got := httpReq.Header.Get("User-Agent"); got != officialCodexUserAgent(auth) {
		t.Fatalf("User-Agent = %q, want account-scoped %q", got, officialCodexUserAgent(auth))
	}
}

func TestCodexNativeIdentityUsesExecutorHeadersWithDynamicCustomHeaders(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	executor := newCodexNativeTestExecutor()
	ctx := contextWithCodexDownstreamHeaders(nil)
	sessionID := uuid.NewString()
	clientHeaders := http.Header{
		"User-Agent":         []string{"codex-shell/1.0.3 (macos; aarch64)"},
		"Session-Id":         []string{sessionID},
		"X-Forwarded-Source": []string{"from-client"},
	}
	auth := newCodexNativeOAuthAuth()
	auth.Attributes = map[string]string{
		"header:X-Forwarded-Test": "$X-Forwarded-Source",
	}
	req := newCodexNativeTestRequest()
	rawJSON := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"hello"}`)

	httpReq, _, _, nativeState, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON, clientHeaders)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if !nativeState.enabled {
		t.Fatal("native identity state was not enabled from executor headers")
	}
	if nativeState.sessionID != sessionID {
		t.Fatalf("session ID = %q, want executor header value %q", nativeState.sessionID, sessionID)
	}

	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg, clientHeaders)
	applyCodexNativeIdentityHeaders(httpReq.Header, &nativeState)
	if got := httpReq.Header.Get("X-Forwarded-Test"); got != "from-client" {
		t.Fatalf("dynamic custom header = %q, want %q", got, "from-client")
	}
	assertCodexNativeHeaders(t, httpReq.Header, nativeState)
}

func TestCodexNativeIdentityIsStablePerSessionAndFreshPerRequest(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	executor := newCodexNativeTestExecutor()
	ctx := contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)")
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	rawJSON := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"hello"}`)

	_, firstBody, _, firstState, firstErr := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON)
	if firstErr != nil {
		t.Fatalf("first cacheHelper error: %v", firstErr)
	}
	_, secondBody, _, secondState, secondErr := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON)
	if secondErr != nil {
		t.Fatalf("second cacheHelper error: %v", secondErr)
	}

	for field, values := range map[string][2]string{
		"installationID": {firstState.installationID, secondState.installationID},
		"sessionID":      {firstState.sessionID, secondState.sessionID},
		"threadID":       {firstState.threadID, secondState.threadID},
		"windowID":       {firstState.windowID, secondState.windowID},
	} {
		if values[0] == "" || values[0] != values[1] {
			t.Fatalf("%s not stable: first=%q second=%q", field, values[0], values[1])
		}
	}
	firstTurnID := gjson.GetBytes(firstBody, "client_metadata.turn_id").String()
	secondTurnID := gjson.GetBytes(secondBody, "client_metadata.turn_id").String()
	if firstTurnID == "" || secondTurnID == "" || firstTurnID == secondTurnID {
		t.Fatalf("turn IDs must be fresh per request: first=%q second=%q", firstTurnID, secondTurnID)
	}
}

func TestCodexNativeInstallationIDPrefersAccountIdentity(t *testing.T) {
	first := &cliproxyauth.Auth{ID: "auth-file-a", Provider: "codex", Metadata: map[string]any{"account_id": "account-1", "access_token": "token-a"}}
	second := &cliproxyauth.Auth{ID: "auth-file-b", Provider: "codex", Metadata: map[string]any{"account_id": "account-1", "access_token": "token-b"}}
	if gotFirst, gotSecond := codexNativeInstallationID(first), codexNativeInstallationID(second); gotFirst == "" || gotFirst != gotSecond {
		t.Fatalf("installation ID must be stable across auth record changes for the same account: first=%q second=%q", gotFirst, gotSecond)
	}
}

func TestCodexNativeIdentityPreservesValidNativeMetadataCanonically(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	executor := newCodexNativeTestExecutor()
	installationID := uuid.NewString()
	sessionID := uuid.NewString()
	threadID := uuid.NewString()
	turnID := uuid.NewString()
	windowID := threadID + ":7"
	turnMetadata := `{"installation_id":"` + installationID + `","session_id":"` + sessionID + `","thread_id":"` + threadID + `","turn_id":"` + turnID + `","window_id":"` + windowID + `","request_kind":"turn","turn_started_at_unix_ms":123,"extra_key":"preserved"}`
	ctx := contextWithCodexDownstreamHeaders(map[string]string{
		"User-Agent":              "codex-shell/1.0.3 (macos; aarch64)",
		"Session-Id":              sessionID,
		"Thread-Id":               threadID,
		"X-Codex-Installation-Id": installationID,
		"X-Codex-Window-Id":       windowID,
		"X-Codex-Turn-Metadata":   turnMetadata,
	})
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	rawJSON := []byte(`{"model":"gpt-5.6-sol","client_metadata":{"x-codex-installation-id":"` + installationID + `","session_id":"` + sessionID + `","thread_id":"` + threadID + `","turn_id":"` + turnID + `","x-codex-window-id":"` + windowID + `","x-codex-turn-metadata":` + quoteJSONString(turnMetadata) + `}}`)

	httpReq, body, _, state, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg)
	applyCodexNativeIdentityHeaders(httpReq.Header, &state)

	if !state.enabled {
		t.Fatal("native identity was not enabled")
	}
	for path, want := range map[string]string{
		"client_metadata.x-codex-installation-id": installationID,
		"client_metadata.session_id":              sessionID,
		"client_metadata.thread_id":               threadID,
		"client_metadata.turn_id":                 turnID,
		"client_metadata.x-codex-window-id":       windowID,
	} {
		if got := gjson.GetBytes(body, path).String(); got != want {
			t.Fatalf("%s = %q, want preserved %q", path, got, want)
		}
	}
	if got := gjson.Get(state.turnMetadata, "turn_started_at_unix_ms").Int(); got != 123 {
		t.Fatalf("turn_started_at_unix_ms = %d, want preserved 123", got)
	}
	if got := gjson.Get(state.turnMetadata, "extra_key").String(); got != "preserved" {
		t.Fatalf("extra_key = %q, want preserved", got)
	}
	assertCodexNativeHeaders(t, httpReq.Header, state)
}

func TestCodexNativeIdentityCanonicalizesConflictingAndMalformedMetadata(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	executor := newCodexNativeTestExecutor()
	validSessionID := uuid.NewString()
	validThreadID := uuid.NewString()
	validTurnID := uuid.NewString()
	ctx := contextWithCodexDownstreamHeaders(map[string]string{
		"User-Agent":              "codex-shell/1.0.3 (macos; aarch64)",
		"Session-Id":              "not-a-uuid",
		"Thread-Id":               validThreadID,
		"X-Codex-Installation-Id": "not-a-uuid",
		"X-Codex-Window-Id":       validThreadID + ":not-a-number",
		"X-Codex-Turn-Metadata":   `{"session_id":"bad","thread_id":"bad","turn_id":"bad","window_id":"bad:0","request_kind":"compact","turn_started_at_unix_ms":-1}`,
	})
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	rawJSON := []byte(`{"model":"gpt-5.6-sol","client_metadata":{"session_id":"` + validSessionID + `","thread_id":"invalid-thread","turn_id":"` + validTurnID + `","x-codex-window-id":"invalid-window"}}`)

	httpReq, body, _, state, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg)
	applyCodexNativeIdentityHeaders(httpReq.Header, &state)

	if state.sessionID != validSessionID {
		t.Fatalf("session ID = %q, want valid body value %q", state.sessionID, validSessionID)
	}
	if state.threadID != validThreadID {
		t.Fatalf("thread ID = %q, want valid header value %q", state.threadID, validThreadID)
	}
	if state.turnID != validTurnID {
		t.Fatalf("turn ID = %q, want valid body value %q", state.turnID, validTurnID)
	}
	if state.installationID != codexNativeInstallationID(auth) {
		t.Fatalf("installation ID = %q, want generated %q", state.installationID, codexNativeInstallationID(auth))
	}
	if state.windowID != validThreadID+":0" {
		t.Fatalf("window ID = %q, want generated %q", state.windowID, validThreadID+":0")
	}
	assertCodexNativeTurnMetadata(t, state.turnMetadata, state)
	assertCodexNativeHeaders(t, httpReq.Header, state)
	for path, want := range map[string]string{
		"client_metadata.session_id":        state.sessionID,
		"client_metadata.thread_id":         state.threadID,
		"client_metadata.turn_id":           state.turnID,
		"client_metadata.x-codex-window-id": state.windowID,
	} {
		if got := gjson.GetBytes(body, path).String(); got != want {
			t.Fatalf("body %s = %q, want canonical %q", path, got, want)
		}
	}
}

func TestCodexNativeIdentityRejectsUnsafeTurnMetadata(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	tests := []struct {
		name     string
		metadata string
	}{
		{name: "invalid JSON", metadata: `{"unterminated":`},
		{name: "non object", metadata: `["not-an-object"]`},
		{name: "oversized", metadata: `{"padding":"` + strings.Repeat("x", codexNativeTurnMetadataMaxBytes) + `"}`},
		{name: "control byte", metadata: "{\"value\":\"bad\x01value\"}"},
		{name: "carriage return whitespace", metadata: "{\r\n\"value\":\"valid-json-but-unsafe-header\"}"},
		{name: "line feed whitespace", metadata: "{\n\"value\":\"valid-json-but-unsafe-header\"}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := newCodexNativeTestExecutor()
			ctx := contextWithCodexDownstreamHeaders(map[string]string{
				"User-Agent":            "codex-shell/1.0.3 (macos; aarch64)",
				"X-Codex-Turn-Metadata": tt.metadata,
			})
			auth := newCodexNativeOAuthAuth()
			req := newCodexNativeTestRequest()
			_, _, _, state, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, []byte(`{"model":"gpt-5.6-sol"}`))
			if err != nil {
				t.Fatalf("cacheHelper error: %v", err)
			}
			if !state.enabled {
				t.Fatal("native identity unexpectedly disabled")
			}
			if gjson.Get(state.turnMetadata, "padding").Exists() || gjson.Get(state.turnMetadata, "value").Exists() || gjson.Get(state.turnMetadata, "unterminated").Exists() {
				t.Fatalf("unsafe metadata was preserved: %q", state.turnMetadata)
			}
			assertCodexNativeTurnMetadata(t, state.turnMetadata, state)
		})
	}
}

func TestCodexNativeIdentityBoundsFinalTurnMetadata(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	paddingLength := codexNativeTurnMetadataMaxBytes - len(`{"padding":""}`)
	metadata := `{"padding":"` + strings.Repeat("x", paddingLength) + `"}`
	executor := newCodexNativeTestExecutor()
	ctx := contextWithCodexDownstreamHeaders(map[string]string{
		"User-Agent":            "codex-shell/1.0.3 (macos; aarch64)",
		"X-Codex-Turn-Metadata": metadata,
	})
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	_, _, _, state, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, []byte(`{"model":"gpt-5.6-sol"}`))
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if !state.enabled {
		t.Fatal("native identity unexpectedly disabled")
	}
	if len(state.turnMetadata) > codexNativeTurnMetadataMaxBytes {
		t.Fatalf("final turn metadata length = %d, want <= %d", len(state.turnMetadata), codexNativeTurnMetadataMaxBytes)
	}
	if gjson.Get(state.turnMetadata, "padding").Exists() {
		t.Fatalf("near-limit optional metadata was not discarded: %s", state.turnMetadata)
	}
	assertCodexNativeTurnMetadata(t, state.turnMetadata, state)
}

func TestCodexNativeIdentityCanonicalizesVersionHeader(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	executor := newCodexNativeTestExecutor()
	ctx := contextWithCodexDownstreamHeaders(map[string]string{
		"User-Agent": "codex-shell/1.0.3 (macos; aarch64)",
		"Version":    "untrusted-client-version",
	})
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	httpReq, _, _, state, err := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, []byte(`{"model":"gpt-5.6-sol"}`))
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, executor.cfg)
	applyCodexNativeIdentityHeaders(httpReq.Header, &state)
	if got := httpReq.Header.Get("Version"); got != codexNativeVersion {
		t.Fatalf("Version = %q, want canonical %q", got, codexNativeVersion)
	}
}

func TestCodexNativeIdentityHonorsModelIdentityHeaderOverrides(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	reg := registry.GetGlobalRegistry()
	clientID := "test-codex-native-identity-header-override"
	model := "test-codex-native-identity-model"
	reg.RegisterClient(clientID, "codex", []*registry.ModelInfo{{
		ID: model,
		Config: &registry.ModelConfig{OverrideHeader: map[string]string{
			"user-agent": "custom-ua/1.0",
		}},
	}})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	body := []byte(`{"model":"` + model + `"}`)
	req := newCodexNativeTestRequest()
	req.Model = model + "(xhigh)"
	req.Payload = body
	gotBody, state := applyCodexNativeIdentityBody(
		contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
		&config.Config{},
		sdktranslator.FormatOpenAIResponse,
		codexNativeResponsesURL,
		newCodexNativeOAuthAuth(),
		req,
		"cache-id",
		body,
	)
	if state.enabled {
		t.Fatal("native identity unexpectedly enabled despite model identity header override")
	}
	if gjson.GetBytes(gotBody, "client_metadata.x-codex-installation-id").Exists() {
		t.Fatalf("native client_metadata unexpectedly added: %s", string(gotBody))
	}
}

func TestCodexNativeIdentityDoesNotApplyToInvalidTranslatedJSON(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	body := []byte(`{"model":`)
	req := newCodexNativeTestRequest()
	gotBody, state := applyCodexNativeIdentityBody(
		contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
		&config.Config{},
		sdktranslator.FormatOpenAIResponse,
		codexNativeResponsesURL,
		newCodexNativeOAuthAuth(),
		req,
		"cache-id",
		body,
	)
	if state.enabled {
		t.Fatal("native identity unexpectedly enabled for invalid translated JSON")
	}
	if string(gotBody) != string(body) {
		t.Fatalf("invalid body changed: got %q want %q", string(gotBody), string(body))
	}
}

func TestCodexNativeIdentityAppliesToOAuthResponsesWithoutClientScope(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol"}`)
	req := newCodexNativeTestRequest()
	gotBody, state := applyCodexNativeIdentityBody(
		contextWithCodexDownstreamUserAgent("curl/8.7.1"),
		&config.Config{},
		sdktranslator.FormatOpenAI,
		codexNativeResponsesURL,
		newCodexNativeOAuthAuth(),
		req,
		"cache-id",
		body,
	)
	if !state.enabled {
		t.Fatal("native identity should apply to Codex OAuth /responses requests")
	}
	if got := gjson.GetBytes(gotBody, "client_metadata.x-codex-installation-id").String(); got == "" {
		t.Fatal("expected native installation id on cloaked OAuth request")
	}
}

func TestCodexNativeIdentityDoesNotApplyOutsideScopedOAuthResponses(t *testing.T) {
	useCodexNativeTestCallerScope(t)
	tests := []struct {
		name string
		cfg  *config.Config
		ctx  context.Context
		from sdktranslator.Format
		url  string
		auth *cliproxyauth.Auth
	}{
		{
			name: "Codex API key",
			cfg:  &config.Config{},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: &cliproxyauth.Auth{ID: "api-key", Provider: "codex", Attributes: map[string]string{cliproxyauth.AttributeAPIKey: "sk-test"}},
		},
		{
			name: "mixed OAuth and API key prefers API key",
			cfg:  &config.Config{},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: &cliproxyauth.Auth{ID: "mixed", Provider: "codex", Attributes: map[string]string{cliproxyauth.AttributeAPIKey: "sk-test", cliproxyauth.AttributeAuthKind: cliproxyauth.AuthKindOAuth}, Metadata: map[string]any{"access_token": "oauth-token"}},
		},
		{
			name: "OAuth without access token",
			cfg:  &config.Config{},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: &cliproxyauth.Auth{ID: "oauth", Provider: "codex", Attributes: map[string]string{cliproxyauth.AttributeAuthKind: cliproxyauth.AuthKindOAuth}, Metadata: map[string]any{"refresh_token": "refresh-only"}},
		},
		{
			name: "unknown Codex auth kind",
			cfg:  &config.Config{},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: &cliproxyauth.Auth{ID: "unknown", Provider: "codex"},
		},
		{
			name: "non Codex provider",
			cfg:  &config.Config{},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: &cliproxyauth.Auth{ID: "oauth", Provider: "openai", Metadata: map[string]any{"access_token": "token"}},
		},
		{
			name: "nil config",
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: newCodexNativeOAuthAuth(),
		},
		{
			name: "cloaking disabled",
			cfg:  &config.Config{Codex: config.CodexConfig{DisableCodexCloaking: true}},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: newCodexNativeOAuthAuth(),
		},
		{
			name: "native identity disabled",
			cfg:  &config.Config{Codex: config.CodexConfig{DisableNativeIdentity: true}},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: newCodexNativeOAuthAuth(),
		},
		{
			name: "identity confuse active",
			cfg:  &config.Config{Routing: config.RoutingConfig{Strategy: "fill-first"}, Codex: config.CodexConfig{IdentityConfuse: true}},
			ctx:  contextWithCodexDownstreamUserAgent("codex-shell/1.0.3 (macos; aarch64)"),
			from: sdktranslator.FormatOpenAIResponse,
			url:  codexNativeResponsesURL,
			auth: newCodexNativeOAuthAuth(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol"}`)
			req := newCodexNativeTestRequest()
			gotBody, state := applyCodexNativeIdentityBody(tt.ctx, tt.cfg, tt.from, tt.url, tt.auth, req, "cache-id", body)
			if state.enabled {
				t.Fatal("native identity unexpectedly enabled")
			}
			if gjson.GetBytes(gotBody, "client_metadata.x-codex-installation-id").Exists() {
				t.Fatalf("native client_metadata unexpectedly added: %s", string(gotBody))
			}
		})
	}
}

func TestCodexDisableNativeIdentityKeepsCloakingHeaders(t *testing.T) {
	auth := newCodexNativeOAuthAuth()
	cfg := &config.Config{Codex: config.CodexConfig{DisableNativeIdentity: true}}
	req := newCodexNativeTestRequest()
	body := []byte(`{"model":"gpt-5.6-sol"}`)
	gotBody, state := applyCodexNativeIdentityBody(
		contextWithCodexDownstreamUserAgent("downstream-client/1.0"),
		cfg,
		sdktranslator.FormatOpenAIResponse,
		codexNativeResponsesURL,
		auth,
		req,
		"cache-id",
		body,
	)
	if state.enabled {
		t.Fatal("native identity unexpectedly enabled")
	}
	if gjson.GetBytes(gotBody, "client_metadata.x-codex-installation-id").Exists() {
		t.Fatalf("native metadata unexpectedly added: %s", string(gotBody))
	}

	httpReq, errReq := http.NewRequest(http.MethodPost, codexNativeResponsesURL, nil)
	if errReq != nil {
		t.Fatalf("NewRequest() error = %v", errReq)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, cfg)
	if got := httpReq.Header.Get("User-Agent"); got != officialCodexUserAgent(auth) {
		t.Fatalf("User-Agent = %q, want cloaked %q", got, officialCodexUserAgent(auth))
	}
	if got := httpReq.Header.Get("Originator"); got != codexOriginator {
		t.Fatalf("Originator = %q, want %q", got, codexOriginator)
	}
}

func TestCodexNativeIdentityMatchesHTTPAndWebsocketSessions(t *testing.T) {
	cfg := &config.Config{}
	auth := newCodexNativeOAuthAuth()
	req := newCodexNativeTestRequest()
	req.Payload = []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"shared-logical-session","input":"hello"}`)
	rawJSON := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"shared-logical-session","input":"hello"}`)
	ctx := contextWithCodexDownstreamHeaders(nil)

	executor := &CodexExecutor{cfg: cfg}
	httpReq, httpBody, _, httpState, errCache := executor.cacheHelper(ctx, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, req.Payload, rawJSON)
	if errCache != nil {
		t.Fatalf("cacheHelper error = %v", errCache)
	}
	applyCodexHeaders(httpReq, auth, "oauth-token", true, cfg)
	applyCodexNativeIdentityHeaders(httpReq.Header, &httpState)

	wsBody, wsHeaders, errPromptCache := applyCodexPromptCacheHeadersWithContext(ctx, sdktranslator.FormatOpenAIResponse, req, rawJSON)
	if errPromptCache != nil {
		t.Fatalf("applyCodexPromptCacheHeadersWithContext error = %v", errPromptCache)
	}
	wsBody, identityState := applyCodexIdentityConfuseBody(cfg, auth, req.Payload, wsBody)
	derivedWSState := deriveCodexNativeIdentityState(ctx, cfg, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, codexSessionHeaderValue(wsHeaders), wsBody, wsHeaders)
	wsBody, wsState := applyCodexNativeIdentityBody(ctx, cfg, sdktranslator.FormatOpenAIResponse, codexNativeResponsesURL, auth, req, codexSessionHeaderValue(wsHeaders), wsBody, wsHeaders)
	if derivedWSState.sessionID != wsState.sessionID || derivedWSState.threadID != wsState.threadID {
		t.Fatalf("pure identity derivation mismatch: derived=%s/%s applied=%s/%s", derivedWSState.sessionID, derivedWSState.threadID, wsState.sessionID, wsState.threadID)
	}
	wsHeaders = applyCodexWebsocketHeaders(ctx, wsHeaders, auth, "oauth-token", cfg, false)
	applyCodexIdentityConfuseHeaders(wsHeaders, &identityState)
	applyCodexNativeIdentityWebsocketHeaders(wsHeaders, &wsState)

	if !httpState.enabled || !wsState.enabled {
		t.Fatalf("native identity not enabled: http=%v websocket=%v", httpState.enabled, wsState.enabled)
	}
	if httpState.sessionID != wsState.sessionID || httpState.threadID != wsState.threadID {
		t.Fatalf("HTTP/WebSocket native identity mismatch: http=%s/%s websocket=%s/%s", httpState.sessionID, httpState.threadID, wsState.sessionID, wsState.threadID)
	}
	if got := gjson.GetBytes(httpBody, "prompt_cache_key").String(); got != httpState.sessionID {
		t.Fatalf("HTTP body prompt_cache_key = %q, want %q", got, httpState.sessionID)
	}
	if got := gjson.GetBytes(wsBody, "prompt_cache_key").String(); got != wsState.sessionID {
		t.Fatalf("WebSocket body prompt_cache_key = %q, want %q", got, wsState.sessionID)
	}
	if got := headerValueCaseInsensitive(httpReq.Header, "Session-Id"); got != httpState.sessionID {
		t.Fatalf("HTTP Session-Id = %q, want %q", got, httpState.sessionID)
	}
	if got := headerValueCaseInsensitive(wsHeaders, "session_id"); got != wsState.sessionID {
		t.Fatalf("WebSocket session_id = %q, want %q", got, wsState.sessionID)
	}
	if _, errParse := uuid.Parse(wsState.sessionID); errParse != nil || wsState.sessionID == "shared-logical-session" {
		t.Fatalf("session ID = %q, want stable UUID derived from non-UUID cache key", wsState.sessionID)
	}
}

func TestOfficialCodexUserAgentIsStableAndDiverse(t *testing.T) {
	first := officialCodexUserAgent(&cliproxyauth.Auth{ID: "auth-a", Metadata: map[string]any{"account_id": "acct-1"}})
	second := officialCodexUserAgent(&cliproxyauth.Auth{ID: "auth-b", Metadata: map[string]any{"account_id": "acct-1"}})
	if first == "" || first != second {
		t.Fatalf("UA not stable for same account: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "codex_cli_rs/"+codexCLIVersion+" ") || !strings.Contains(first, "(codex-tui; "+codexCLIVersion+")") {
		t.Fatalf("UA = %q, want official originator/version shape", first)
	}
	unique := map[string]struct{}{}
	for i := 0; i < 24; i++ {
		ua := officialCodexUserAgent(&cliproxyauth.Auth{Metadata: map[string]any{"account_id": "acct-" + strconv.Itoa(i)}})
		unique[ua] = struct{}{}
	}
	if len(unique) < 2 {
		t.Fatalf("expected per-account UA diversity, got %#v", unique)
	}
	if officialCodexUserAgent(nil) != codexUserAgent {
		t.Fatalf("nil auth UA = %q, want default %q", officialCodexUserAgent(nil), codexUserAgent)
	}
}

func newCodexNativeTestExecutor() *CodexExecutor {
	return &CodexExecutor{cfg: &config.Config{}}
}

func newCodexNativeOAuthAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       "codex-oauth-1",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": "oauth-token",
			"account_id":   "account-1",
		},
	}
}

func newCodexNativeTestRequest() cliproxyexecutor.Request {
	return cliproxyexecutor.Request{
		Model:    "gpt-5.6-sol",
		Payload:  []byte(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"hello"}]}`),
		Metadata: map[string]any{cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:codex-thread-1"},
	}
}

func contextWithCodexDownstreamUserAgent(userAgent string) context.Context {
	return contextWithCodexDownstreamHeaders(map[string]string{"User-Agent": userAgent})
}

func contextWithCodexDownstreamHeaders(headers map[string]string) context.Context {
	return contextWithCodexDownstreamHeadersAndCredential(headers, codexNativeTestCredential)
}

func contextWithCodexDownstreamHeadersAndCredential(headers map[string]string, callerCredential string) context.Context {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	for name, value := range headers {
		ginCtx.Request.Header.Set(name, value)
	}
	if strings.TrimSpace(callerCredential) != "" {
		ginCtx.Set("userApiKey", callerCredential)
	}
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func useCodexNativeTestCallerScope(t *testing.T) {
	t.Helper()
}

func assertCodexNativeHeaders(t *testing.T, headers http.Header, state codexNativeIdentityState) {
	t.Helper()
	for headerName, want := range map[string]string{
		"Originator":              codexNativeOriginator,
		"Version":                 codexNativeVersion,
		"Session-Id":              state.sessionID,
		"Thread-Id":               state.threadID,
		"X-Client-Request-Id":     state.threadID,
		"X-Codex-Installation-Id": state.installationID,
		"X-Codex-Window-Id":       state.windowID,
		"X-Codex-Turn-Metadata":   state.turnMetadata,
	} {
		if got := headerValueCaseInsensitive(headers, headerName); got != want {
			t.Fatalf("%s = %q, want %q", headerName, got, want)
		}
	}
}

func assertCodexNativeTurnMetadata(t *testing.T, rawMetadata string, state codexNativeIdentityState) {
	t.Helper()
	if !gjson.Valid(rawMetadata) || !gjson.Parse(rawMetadata).IsObject() {
		t.Fatalf("turn metadata is not a valid JSON object: %q", rawMetadata)
	}
	for path, want := range map[string]string{
		"installation_id": state.installationID,
		"session_id":      state.sessionID,
		"thread_id":       state.threadID,
		"turn_id":         state.turnID,
		"window_id":       state.windowID,
		"request_kind":    "turn",
	} {
		if got := gjson.Get(rawMetadata, path).String(); got != want {
			t.Fatalf("turn metadata %s = %q, want %q; metadata=%s", path, got, want, rawMetadata)
		}
	}
	if got := gjson.Get(rawMetadata, "turn_started_at_unix_ms").Int(); got <= 0 {
		t.Fatalf("turn_started_at_unix_ms = %d, want positive; metadata=%s", got, rawMetadata)
	}
}

func assertCodexNativeUUID(t *testing.T, name, value string) {
	t.Helper()
	if _, errParse := uuid.Parse(value); errParse != nil {
		t.Fatalf("%s = %q, want UUID: %v", name, value, errParse)
	}
}

func quoteJSONString(value string) string {
	return strconv.Quote(value)
}
