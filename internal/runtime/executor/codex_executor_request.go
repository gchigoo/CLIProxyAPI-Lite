package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	codexCLIVersion                 = "0.154.0"
	codexUserAgent                  = "codex_cli_rs/" + codexCLIVersion + " (Mac OS 26.5.2; arm64) Apple_Terminal/470 (codex-tui; " + codexCLIVersion + ")"
	codexOriginator                 = "codex_cli_rs"
	codexDefaultImageToolModel      = "gpt-image-2"
	codexResponsesLiteHeader        = "X-OpenAI-Internal-Codex-Responses-Lite"
	codexNativeOriginator           = codexOriginator
	codexNativeVersion              = codexCLIVersion
	codexNativeTurnMetadataMaxBytes = 8 * 1024
)

var dataTag = []byte("data:")

func translateCodexRequestPair(from, to sdktranslator.Format, model string, originalPayload, payload []byte, stream bool, preserveEmptyThinkingBlocks ...bool) ([]byte, []byte) {
	isCompat := len(preserveEmptyThinkingBlocks) > 0 && preserveEmptyThinkingBlocks[0]
	translate := func(raw []byte) []byte {
		if isCompat && from == sdktranslator.FormatClaude && to == sdktranslator.FormatCodex {
			return helps.TranslateRequestWithAPIKeyModelCompatibility(context.Background(), nil, nil, from, to, model, raw, stream, true)
		}
		return sdktranslator.TranslateRequest(from, to, model, raw, stream)
	}
	if bytes.Equal(originalPayload, payload) {
		body := translate(payload)
		return body, body
	}
	originalTranslated := translate(originalPayload)
	body := translate(payload)
	return originalTranslated, body
}

// PrepareRequest injects Codex credentials into the outgoing HTTP request.
func (e *CodexExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	apiKey, _ := codexCreds(auth)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else {
		req.Header.Del("Authorization")
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects Codex credentials into the request and executes it.
func (e *CodexExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("codex executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

type codexIdentityConfuseState struct {
	enabled                bool
	authID                 string
	originalPromptCacheKey string
	promptCacheKey         string
	turnIDs                []codexIdentityReplacement
}

type codexIdentityReplacement struct {
	original string
	confused string
}

func (e *CodexExecutor) cacheHelper(ctx context.Context, from sdktranslator.Format, url string, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, userPayload []byte, rawJSON []byte, headerSets ...http.Header) (*http.Request, []byte, codexIdentityConfuseState, codexNativeIdentityState, error) {
	var headers http.Header
	if len(headerSets) > 0 {
		headers = headerSets[0]
	}
	var cache helps.CodexCache
	if sourceFormatEqual(from, sdktranslator.FormatClaude) {
		modelName := strings.TrimSpace(gjson.GetBytes(rawJSON, "model").String())
		if modelName == "" {
			modelName = thinking.ParseSuffix(req.Model).ModelName
		}
		cached, ok, errCache := helps.ClaudeCodePromptCache(ctx, modelName, req.Payload, headers)
		if errCache != nil {
			return nil, nil, codexIdentityConfuseState{}, codexNativeIdentityState{}, errCache
		}
		if ok {
			cache = cached
		}
	} else if sourceFormatEqual(from, sdktranslator.FormatOpenAIResponse) {
		promptCacheKey := gjson.GetBytes(req.Payload, "prompt_cache_key")
		if promptCacheKey.Exists() {
			cache.ID = promptCacheKey.String()
		}
	} else if sourceFormatEqual(from, sdktranslator.FormatOpenAI) || strings.EqualFold(strings.TrimSpace(from.String()), codexOpenAIImageSourceFormat) {
		if promptCacheKey := gjson.GetBytes(req.Payload, "prompt_cache_key"); promptCacheKey.Exists() {
			cache.ID = strings.TrimSpace(promptCacheKey.String())
		}
		if cache.ID == "" {
			cache.ID = helps.ProviderSessionUUID("codex", req.Metadata)
		}
		if cache.ID == "" {
			if apiKey := strings.TrimSpace(helps.APIKeyFromContext(ctx)); apiKey != "" {
				cache.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex:prompt-cache:"+apiKey)).String()
			}
		}
	}
	if cache.ID == "" {
		cache.ID = helps.ProviderSessionUUID("codex", req.Metadata)
	}

	if cache.ID != "" {
		rawJSON = helps.SetStringIfDifferent(rawJSON, "prompt_cache_key", cache.ID)
	}
	rawJSON = helps.SanitizeCodexInputItemIDs(rawJSON)
	var identityState codexIdentityConfuseState
	rawJSON, identityState = applyCodexIdentityConfuseBody(e.cfg, auth, userPayload, rawJSON)
	if identityState.promptCacheKey != "" {
		cache.ID = identityState.promptCacheKey
	}
	var nativeIdentityState codexNativeIdentityState
	rawJSON, nativeIdentityState = applyCodexNativeIdentityBody(ctx, e.cfg, from, url, auth, req, cache.ID, rawJSON, headers)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawJSON))
	if err != nil {
		return nil, nil, codexIdentityConfuseState{}, codexNativeIdentityState{}, err
	}
	if cache.ID != "" {
		httpReq.Header.Set("Session-Id", cache.ID)
	}
	return httpReq, rawJSON, identityState, nativeIdentityState, nil
}

func applyCodexIdentityConfuseBody(cfg *config.Config, auth *cliproxyauth.Auth, userPayload []byte, rawJSON []byte) ([]byte, codexIdentityConfuseState) {
	if !codexIdentityConfuseEnabled(cfg) || auth == nil || strings.TrimSpace(auth.ID) == "" || len(rawJSON) == 0 {
		return rawJSON, codexIdentityConfuseState{}
	}

	state := codexIdentityConfuseState{enabled: true, authID: strings.TrimSpace(auth.ID)}
	if promptCacheKey := strings.TrimSpace(gjson.GetBytes(userPayload, "prompt_cache_key").String()); promptCacheKey != "" {
		state.originalPromptCacheKey = promptCacheKey
		state.promptCacheKey = codexIdentityConfuseUUID(auth.ID, "prompt-cache", promptCacheKey)
		rawJSON = helps.SetStringIfDifferent(rawJSON, "prompt_cache_key", state.promptCacheKey)
	}
	if installationID := strings.TrimSpace(gjson.GetBytes(userPayload, "client_metadata.x-codex-installation-id").String()); installationID != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-installation-id", codexIdentityConfuseUUID(auth.ID, "installation", installationID))
	}
	if turnMetadata := strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-turn-metadata").String()); turnMetadata != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-turn-metadata", applyCodexTurnMetadataIdentityConfuse(turnMetadata, &state))
	}
	if state.promptCacheKey != "" {
		if windowID := strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-window-id").String()); windowID != "" {
			rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-window-id", state.promptCacheKey+":0")
		}
	}

	return rawJSON, state
}

func applyCodexIdentityConfuseHeaders(headers http.Header, state *codexIdentityConfuseState) {
	if headers == nil {
		return
	}
	if state == nil || !state.enabled {
		return
	}

	if rawTurnMetadata := strings.TrimSpace(headers.Get("X-Codex-Turn-Metadata")); rawTurnMetadata != "" {
		headers.Set("X-Codex-Turn-Metadata", applyCodexTurnMetadataIdentityConfuse(rawTurnMetadata, state))
	}
	if state.promptCacheKey == "" {
		return
	}

	setCodexSessionHeaderCasePreserved(headers, "Session-Id", state.promptCacheKey)
	if headerValueCaseInsensitive(headers, "Conversation_id") != "" {
		setHeaderCasePreserved(headers, "Conversation_id", state.promptCacheKey)
	}
	headers.Set("X-Client-Request-Id", state.promptCacheKey)
	headers.Set("Thread-Id", state.promptCacheKey)
	headers.Set("X-Codex-Window-Id", state.promptCacheKey+":0")
}

func applyCodexTurnMetadataIdentityConfuse(rawTurnMetadata string, state *codexIdentityConfuseState) string {
	updatedTurnMetadata := rawTurnMetadata
	if state == nil || !state.enabled {
		return updatedTurnMetadata
	}
	if state.promptCacheKey != "" && gjson.Get(rawTurnMetadata, "prompt_cache_key").Exists() {
		updatedTurnMetadata, _ = sjson.Set(updatedTurnMetadata, "prompt_cache_key", state.promptCacheKey)
	} else if state.promptCacheKey != "" && state.originalPromptCacheKey != "" {
		updatedTurnMetadata = strings.ReplaceAll(updatedTurnMetadata, state.originalPromptCacheKey, state.promptCacheKey)
	}
	if turnID := strings.TrimSpace(gjson.Get(rawTurnMetadata, "turn_id").String()); turnID != "" {
		updatedTurnMetadata, _ = sjson.Set(updatedTurnMetadata, "turn_id", state.confuseTurnID(turnID))
	}
	if state.promptCacheKey != "" && gjson.Get(rawTurnMetadata, "window_id").Exists() {
		updatedTurnMetadata, _ = sjson.Set(updatedTurnMetadata, "window_id", state.promptCacheKey+":0")
	}
	return updatedTurnMetadata
}

func applyCodexIdentityConfuseResponsePayload(payload []byte, state codexIdentityConfuseState) []byte {
	payload = replaceCodexIdentityResponsePayload(payload, state.originalPromptCacheKey, state.promptCacheKey)
	for _, turnID := range state.turnIDs {
		payload = replaceCodexIdentityResponsePayload(payload, turnID.original, turnID.confused)
	}
	return payload
}

func applyCodexIdentityExposeResponsePayload(payload []byte, state codexIdentityConfuseState) []byte {
	payload = replaceCodexIdentityResponsePayload(payload, state.promptCacheKey, state.originalPromptCacheKey)
	for _, turnID := range state.turnIDs {
		payload = replaceCodexIdentityResponsePayload(payload, turnID.confused, turnID.original)
	}
	return payload
}

func (state *codexIdentityConfuseState) confuseTurnID(turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if state == nil || !state.enabled || strings.TrimSpace(state.authID) == "" || turnID == "" {
		return turnID
	}
	for _, replacement := range state.turnIDs {
		if replacement.original == turnID || replacement.confused == turnID {
			return replacement.confused
		}
	}
	confusedTurnID := codexIdentityConfuseUUID(state.authID, "turn", turnID)
	state.turnIDs = append(state.turnIDs, codexIdentityReplacement{original: turnID, confused: confusedTurnID})
	return confusedTurnID
}

func replaceCodexIdentityResponsePayload(payload []byte, from string, to string) []byte {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if len(payload) == 0 || from == "" || to == "" || from == to || !bytes.Contains(payload, []byte(from)) {
		return payload
	}
	return bytes.ReplaceAll(payload, []byte(from), []byte(to))
}

func codexIdentityConfuseEnabled(cfg *config.Config) bool {
	if cfg == nil || !cfg.Codex.IdentityConfuse {
		return false
	}
	strategy := strings.ToLower(strings.TrimSpace(cfg.Routing.Strategy))
	return cfg.Routing.SessionAffinity || strategy == "fill-first" || strategy == "fillfirst" || strategy == "ff"
}

func codexIdentityConfuseUUID(authID string, kind string, value string) string {
	name := strings.Join([]string{"cli-proxy-api", "codex", "identity-confuse", kind, strings.TrimSpace(authID), strings.TrimSpace(value)}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

type codexNativeIdentityState struct {
	enabled        bool
	installationID string
	sessionID      string
	threadID       string
	turnID         string
	windowID       string
	turnMetadata   string
	userAgent      string
}

func applyCodexNativeIdentityBody(ctx context.Context, cfg *config.Config, from sdktranslator.Format, requestURL string, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, cacheID string, rawJSON []byte, headerSets ...http.Header) ([]byte, codexNativeIdentityState) {
	headers := downstreamHeaders(ctx, headerSets...)
	state := deriveCodexNativeIdentityState(ctx, cfg, from, requestURL, auth, req, cacheID, rawJSON, headers)
	if !state.enabled {
		return rawJSON, codexNativeIdentityState{}
	}

	rawJSON = setCodexNativeBodyString(rawJSON, "prompt_cache_key", state.sessionID)
	rawJSON = setCodexNativeBodyString(rawJSON, "client_metadata.x-codex-installation-id", state.installationID)
	rawJSON = setCodexNativeBodyString(rawJSON, "client_metadata.session_id", state.sessionID)
	rawJSON = setCodexNativeBodyString(rawJSON, "client_metadata.thread_id", state.threadID)
	rawJSON = setCodexNativeBodyString(rawJSON, "client_metadata.turn_id", state.turnID)
	rawJSON = setCodexNativeBodyString(rawJSON, "client_metadata.x-codex-window-id", state.windowID)
	rawJSON = setCodexNativeBodyString(rawJSON, "client_metadata.x-codex-turn-metadata", state.turnMetadata)
	return rawJSON, state
}

func deriveCodexNativeIdentityState(ctx context.Context, cfg *config.Config, from sdktranslator.Format, requestURL string, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, cacheID string, rawJSON []byte, headers http.Header) codexNativeIdentityState {
	if !shouldSynthesizeCodexNativeIdentity(ctx, cfg, from, requestURL, auth, req.Model, headers) || len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) || !gjson.ParseBytes(rawJSON).IsObject() {
		return codexNativeIdentityState{}
	}
	rawTurnMetadata := firstValidCodexNativeTurnMetadata(
		headerValueCaseInsensitive(headers, "X-Codex-Turn-Metadata"),
		gjson.GetBytes(rawJSON, "client_metadata.x-codex-turn-metadata").String(),
		gjson.GetBytes(req.Payload, "client_metadata.x-codex-turn-metadata").String(),
	)

	defaultSessionID := stableCodexNativeUUID("session", firstNonEmptyCodexIdentityValue(cacheID, helps.ProviderSessionUUID("codex", req.Metadata)))
	sessionID := firstValidCodexNativeUUID(
		codexSessionHeaderValue(headers),
		gjson.GetBytes(rawJSON, "client_metadata.session_id").String(),
		gjson.GetBytes(req.Payload, "client_metadata.session_id").String(),
		gjson.Get(rawTurnMetadata, "session_id").String(),
		defaultSessionID,
	)
	threadID := firstValidCodexNativeUUID(
		headerValueCaseInsensitive(headers, "Thread-Id"),
		gjson.GetBytes(rawJSON, "client_metadata.thread_id").String(),
		gjson.GetBytes(req.Payload, "client_metadata.thread_id").String(),
		gjson.Get(rawTurnMetadata, "thread_id").String(),
		sessionID,
	)
	installationID := firstValidCodexNativeUUID(
		headerValueCaseInsensitive(headers, "X-Codex-Installation-Id"),
		gjson.GetBytes(rawJSON, "client_metadata.x-codex-installation-id").String(),
		gjson.GetBytes(req.Payload, "client_metadata.x-codex-installation-id").String(),
		gjson.Get(rawTurnMetadata, "installation_id").String(),
		codexNativeInstallationID(auth),
	)
	turnID := firstValidCodexNativeUUID(
		gjson.GetBytes(rawJSON, "client_metadata.turn_id").String(),
		gjson.GetBytes(req.Payload, "client_metadata.turn_id").String(),
		gjson.Get(rawTurnMetadata, "turn_id").String(),
		uuid.NewString(),
	)
	windowID := firstValidCodexNativeWindowID(threadID,
		headerValueCaseInsensitive(headers, "X-Codex-Window-Id"),
		gjson.GetBytes(rawJSON, "client_metadata.x-codex-window-id").String(),
		gjson.GetBytes(req.Payload, "client_metadata.x-codex-window-id").String(),
		gjson.Get(rawTurnMetadata, "window_id").String(),
		threadID+":0",
	)
	if installationID == "" || sessionID == "" || threadID == "" || turnID == "" || windowID == "" {
		return codexNativeIdentityState{}
	}

	return codexNativeIdentityState{
		enabled:        true,
		installationID: installationID,
		sessionID:      sessionID,
		threadID:       threadID,
		turnID:         turnID,
		windowID:       windowID,
		turnMetadata:   fillCodexNativeTurnMetadata(rawTurnMetadata, installationID, sessionID, threadID, turnID, windowID),
		userAgent:      officialCodexUserAgent(auth),
	}
}

func applyCodexNativeIdentityHeaders(headers http.Header, state *codexNativeIdentityState) {
	if headers == nil || state == nil || !state.enabled {
		return
	}

	headers.Set("User-Agent", state.userAgent)
	headers.Set("Originator", codexNativeOriginator)
	headers.Set("Version", codexNativeVersion)
	setCodexSessionHeaderCasePreserved(headers, "Session-Id", state.sessionID)
	setHeaderCasePreserved(headers, "Thread-Id", state.threadID)
	setHeaderCasePreserved(headers, "X-Client-Request-Id", state.threadID)
	setHeaderCasePreserved(headers, "X-Codex-Installation-Id", state.installationID)
	setHeaderCasePreserved(headers, "X-Codex-Window-Id", state.windowID)
	setHeaderCasePreserved(headers, "X-Codex-Turn-Metadata", state.turnMetadata)
}

func applyCodexNativeIdentityWebsocketHeaders(headers http.Header, state *codexNativeIdentityState) {
	if headers == nil || state == nil || !state.enabled {
		return
	}

	headers.Set("User-Agent", state.userAgent)
	headers.Set("Originator", codexNativeOriginator)
	headers.Set("Version", codexNativeVersion)
	setCodexSessionHeaderCasePreserved(headers, "session_id", state.sessionID)
	setHeaderCasePreserved(headers, "Conversation_id", state.sessionID)
	setHeaderCasePreserved(headers, "Thread-Id", state.threadID)
	setHeaderCasePreserved(headers, "X-Client-Request-Id", state.threadID)
	setHeaderCasePreserved(headers, "X-Codex-Turn-Metadata", state.turnMetadata)
}

func shouldSynthesizeCodexNativeIdentity(ctx context.Context, cfg *config.Config, from sdktranslator.Format, requestURL string, auth *cliproxyauth.Auth, model string, headers http.Header) bool {
	if cfg == nil || cfg.Codex.DisableCodexCloaking || cfg.Codex.DisableNativeIdentity || codexIdentityConfuseEnabled(cfg) || codexNativeIdentityHeaderOverrideConflict(model) {
		return false
	}
	if !codexNativeResponsesEndpoint(requestURL) {
		return false
	}
	return codexOAuthAccessTokenUsed(auth)
}

func codexNativeResponsesEndpoint(requestURL string) bool {
	parsed, errParse := url.Parse(strings.TrimSpace(requestURL))
	if errParse != nil {
		return false
	}
	trimmed := strings.TrimRight(parsed.Path, "/")
	return strings.HasSuffix(trimmed, "/responses") || strings.HasSuffix(trimmed, "/responses/compact")
}

func codexNativeIdentityHeaderOverrideConflict(model string) bool {
	model = thinking.ParseSuffix(strings.TrimSpace(model)).ModelName
	overrides := registry.ModelOverrideHeaders(model)
	for key, value := range overrides {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "user-agent":
			trimmed := strings.TrimSpace(value)
			if trimmed != "" && !strings.HasPrefix(strings.ToLower(trimmed), "codex_cli_rs/") {
				return true
			}
		case "originator":
			trimmed := strings.TrimSpace(value)
			if trimmed != "" && !strings.EqualFold(trimmed, codexNativeOriginator) {
				return true
			}
		case "version":
			trimmed := strings.TrimSpace(value)
			if trimmed != "" && trimmed != codexNativeVersion {
				return true
			}
		case "session-id", "session_id", "thread-id", "x-client-request-id", "x-codex-installation-id", "x-codex-window-id", "x-codex-turn-metadata":
			return true
		}
	}
	return false
}

func codexOAuthAccessTokenUsed(auth *cliproxyauth.Auth) bool {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") || auth.AuthKind() != cliproxyauth.AuthKindOAuth {
		return false
	}
	if auth.Attributes != nil && strings.TrimSpace(auth.Attributes[cliproxyauth.AttributeAPIKey]) != "" {
		return false
	}
	if auth.Metadata == nil {
		return false
	}
	accessToken, _ := auth.Metadata["access_token"].(string)
	return strings.TrimSpace(accessToken) != ""
}

func downstreamHeaders(ctx context.Context, headerSets ...http.Header) http.Header {
	if len(headerSets) > 0 && headerSets[0] != nil {
		return headerSets[0]
	}
	if ctx == nil {
		return nil
	}
	ginCtx, ok := ctx.Value("gin").(*gin.Context)
	if !ok || ginCtx == nil || ginCtx.Request == nil {
		return nil
	}
	return ginCtx.Request.Header
}

func codexNativeInstallationID(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	credentialID := ""
	if auth.Metadata != nil {
		if accountID, ok := auth.Metadata["account_id"].(string); ok {
			credentialID = strings.TrimSpace(accountID)
		}
	}
	if credentialID == "" {
		credentialID = strings.TrimSpace(auth.ID)
	}
	return stableCodexNativeUUID("installation", credentialID)
}

func stableCodexNativeUUID(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, errParse := uuid.Parse(value); errParse == nil {
		return parsed.String()
	}
	name := strings.Join([]string{"cli-proxy-api", "codex", "native-identity", strings.TrimSpace(kind), value}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

func firstValidCodexNativeTurnMetadata(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > codexNativeTurnMetadataMaxBytes || !validCodexNativeHeaderValue(value) || !gjson.Valid(value) || !gjson.Parse(value).IsObject() {
			continue
		}
		return value
	}
	return `{}`
}

func fillCodexNativeTurnMetadata(rawMetadata, installationID, sessionID, threadID, turnID, windowID string) string {
	metadata := fillCodexNativeTurnMetadataObject(firstValidCodexNativeTurnMetadata(rawMetadata), installationID, sessionID, threadID, turnID, windowID)
	if len(metadata) <= codexNativeTurnMetadataMaxBytes && validCodexNativeHeaderValue(metadata) {
		return metadata
	}
	metadata = fillCodexNativeTurnMetadataObject(`{}`, installationID, sessionID, threadID, turnID, windowID)
	if len(metadata) <= codexNativeTurnMetadataMaxBytes && validCodexNativeHeaderValue(metadata) {
		return metadata
	}
	return `{"installation_id":"` + installationID + `","session_id":"` + sessionID + `","thread_id":"` + threadID + `","turn_id":"` + turnID + `","window_id":"` + windowID + `","request_kind":"turn","turn_started_at_unix_ms":` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `}`
}

func fillCodexNativeTurnMetadataObject(metadata, installationID, sessionID, threadID, turnID, windowID string) string {
	metadata, _ = sjson.Set(metadata, "installation_id", installationID)
	metadata, _ = sjson.Set(metadata, "session_id", sessionID)
	metadata, _ = sjson.Set(metadata, "thread_id", threadID)
	metadata, _ = sjson.Set(metadata, "turn_id", turnID)
	metadata, _ = sjson.Set(metadata, "window_id", windowID)
	metadata, _ = sjson.Set(metadata, "request_kind", "turn")
	startedAt := gjson.Get(metadata, "turn_started_at_unix_ms")
	if startedAt.Type != gjson.Number || startedAt.Int() <= 0 {
		metadata, _ = sjson.Set(metadata, "turn_started_at_unix_ms", time.Now().UnixMilli())
	}
	return metadata
}

func validCodexNativeHeaderValue(value string) bool {
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0x7f || r < 0x20 && r != '\t' {
			return false
		}
	}
	return true
}

func firstValidCodexNativeUUID(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 {
			continue
		}
		parsed, errParse := uuid.Parse(value)
		if errParse == nil {
			return parsed.String()
		}
	}
	return ""
}

func firstValidCodexNativeWindowID(threadID string, values ...string) string {
	prefix := strings.TrimSpace(threadID) + ":"
	if prefix == ":" {
		return ""
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 160 || !strings.HasPrefix(value, prefix) {
			continue
		}
		windowNumber := strings.TrimPrefix(value, prefix)
		parsed, errParse := strconv.ParseUint(windowNumber, 10, 32)
		if errParse == nil {
			return prefix + strconv.FormatUint(parsed, 10)
		}
	}
	return ""
}

func setCodexNativeBodyString(body []byte, path, value string) []byte {
	if strings.TrimSpace(value) == "" {
		return body
	}
	updated, errSet := sjson.SetBytes(body, path, value)
	if errSet != nil {
		return body
	}
	return updated
}

func firstNonEmptyCodexIdentityValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func applyCodexHeaders(r *http.Request, auth *cliproxyauth.Auth, token string, stream bool, cfg *config.Config, clientHeaders ...http.Header) {
	var ginHeaders http.Header
	if len(clientHeaders) > 0 && clientHeaders[0] != nil {
		ginHeaders = clientHeaders[0]
	} else if ginCtx, ok := r.Context().Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
		ginHeaders = ginCtx.Request.Header
	}
	applyCodexHeadersFromSources(r, auth, token, stream, cfg, ginHeaders)
}

// applyModelHeaderOverrides forces models.json config.override_header onto upstream headers.
func applyModelHeaderOverrides(headers http.Header, modelName string) {
	if headers == nil {
		return
	}
	overrides := registry.ModelOverrideHeaders(modelName)
	if len(overrides) == 0 {
		return
	}
	for key, value := range overrides {
		if isStaleCodexTuiIdentityOverride(key, value) {
			continue
		}
		headers.Set(key, value)
	}
	if strings.Contains(headers.Get("User-Agent"), "Mac OS") && codexSessionHeaderValue(headers) == "" {
		headers.Set("Session_id", uuid.NewString())
	}
}

// applyCodexDirectImageHeaders sets Codex upstream headers for direct /images/* calls.
// Downstream client User-Agent values are not forwarded to reduce Cloudflare 1010 blocks.
func applyCodexDirectImageHeaders(r *http.Request, auth *cliproxyauth.Auth, token string, stream bool, cfg *config.Config, clientHeaders ...http.Header) {
	var ginHeaders http.Header
	if len(clientHeaders) > 0 && clientHeaders[0] != nil {
		ginHeaders = clientHeaders[0].Clone()
		ginHeaders.Del("User-Agent")
	} else if ginCtx, ok := r.Context().Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
		ginHeaders = ginCtx.Request.Header.Clone()
		ginHeaders.Del("User-Agent")
	}
	applyCodexHeadersFromSources(r, auth, token, stream, cfg, ginHeaders)
}

func applyCodexHeadersFromSources(r *http.Request, auth *cliproxyauth.Auth, token string, stream bool, cfg *config.Config, ginHeaders http.Header) {
	r.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(token) != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	} else {
		r.Header.Del("Authorization")
	}

	if ginHeaders != nil && ginHeaders.Get("X-Codex-Beta-Features") != "" {
		r.Header.Set("X-Codex-Beta-Features", ginHeaders.Get("X-Codex-Beta-Features"))
	}
	misc.EnsureHeader(r.Header, ginHeaders, "Version", "")
	misc.EnsureHeader(r.Header, ginHeaders, "X-Codex-Turn-Metadata", "")
	misc.EnsureHeader(r.Header, ginHeaders, "X-Codex-Turn-State", "")
	misc.EnsureHeader(r.Header, ginHeaders, "X-Client-Request-Id", "")
	misc.EnsureHeader(r.Header, ginHeaders, "X-Codex-Window-Id", "")
	misc.EnsureHeader(r.Header, ginHeaders, "Thread-Id", "")
	misc.EnsureHeader(r.Header, ginHeaders, "Session-Id", "")
	misc.EnsureHeader(r.Header, ginHeaders, "X-Openai-Internal-Codex-Responses-Lite", "")

	cfgUserAgent, _ := codexHeaderDefaults(cfg, auth)
	ensureHeaderWithConfigPrecedence(r.Header, ginHeaders, "User-Agent", cfgUserAgent, codexUserAgent)

	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
	r.Header.Set("Connection", "Keep-Alive")

	isAPIKey := codexAuthUsesAPIKey(auth)
	if originator := strings.TrimSpace(ginHeaders.Get("Originator")); originator != "" {
		r.Header.Set("Originator", originator)
	} else if !isAPIKey {
		r.Header.Set("Originator", codexOriginator)
	}
	if !isAPIKey {
		if auth != nil && auth.Metadata != nil {
			if accountID, ok := auth.Metadata["account_id"].(string); ok {
				r.Header.Set("Chatgpt-Account-Id", accountID)
			}
		}
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(r, attrs, ginHeaders)
	applyCodexCloakingHeaders(r.Header, cfg, auth)
}

type codexDeviceProfile struct {
	osName    string
	osVersion string
	arch      string
	terminal  string
}

// Per-account profiles keep three Codex OAuth credentials from sharing one UA
// cluster fingerprint. Selection is SHA-256(account_id) so it is stable across
// restarts (sub2api PR #1415).
var codexDeviceProfiles = []codexDeviceProfile{
	{"Mac OS", "26.5.2", "arm64", "Apple_Terminal/470"},
	{"Mac OS", "15.6.0", "x86_64", "iTerm.app/3.6.11"},
	{"Linux", "6.8.0-86-generic", "x86_64", "xterm-256color"},
}

func isStaleCodexTuiIdentityOverride(key, value string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "originator":
		v := strings.ToLower(strings.TrimSpace(value))
		return v == "codex-tui" || v == "codex_cli_rs"
	case "user-agent":
		lower := strings.ToLower(strings.TrimSpace(value))
		return strings.HasPrefix(lower, "codex-tui/") || strings.HasPrefix(lower, "codex_cli_rs/")
	default:
		return false
	}
}

func officialCodexUserAgent(auth *cliproxyauth.Auth) string {
	profile := selectCodexDeviceProfile(auth)
	return fmt.Sprintf("%s/%s (%s %s; %s) %s (codex-tui; %s)",
		codexOriginator, codexCLIVersion, profile.osName, profile.osVersion, profile.arch, profile.terminal, codexCLIVersion)
}

func selectCodexDeviceProfile(auth *cliproxyauth.Auth) codexDeviceProfile {
	id := ""
	if auth != nil {
		if auth.Metadata != nil {
			if accountID, ok := auth.Metadata["account_id"].(string); ok {
				id = strings.TrimSpace(accountID)
			}
		}
		if id == "" {
			id = strings.TrimSpace(auth.ID)
		}
	}
	if id == "" {
		return codexDeviceProfiles[0]
	}
	sum := sha256.Sum256([]byte("cli-proxy-api:codex:device-profile:" + id))
	return codexDeviceProfiles[int(sum[0])%len(codexDeviceProfiles)]
}

func applyCodexCloakingHeaders(headers http.Header, cfg *config.Config, auth *cliproxyauth.Auth) {
	if headers == nil || cfg == nil || cfg.Codex.DisableCodexCloaking {
		return
	}
	headers.Set("User-Agent", officialCodexUserAgent(auth))
	headers.Set("Originator", codexOriginator)
	headers.Set("Version", codexCLIVersion)
}

func normalizeCodexInstructions(body []byte, nativeRequest ...bool) []byte {
	if len(nativeRequest) > 0 && nativeRequest[0] {
		return body
	}
	instructions := gjson.GetBytes(body, "instructions")
	if !instructions.Exists() || instructions.Type == gjson.Null {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	return body
}

var imageGenToolJSON = []byte(`{"type":"image_generation","output_format":"png"}`)
var imageGenToolArrayJSON = []byte(`[{"type":"image_generation","output_format":"png"}]`)

func isCodexFreePlanAuth(auth *cliproxyauth.Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(auth.Attributes["plan_type"]), "free")
}

func isImageGenerationFunctionTool(tool gjson.Result) bool {
	switch tool.Get("type").String() {
	case "function":
		return tool.Get("name").String() == "image_gen.imagegen"
	case "namespace":
		if tool.Get("name").String() != "image_gen" {
			return false
		}
		tools := tool.Get("tools")
		if !tools.IsArray() {
			return false
		}
		for _, nestedTool := range tools.Array() {
			if nestedTool.Get("type").String() == "function" && nestedTool.Get("name").String() == "imagegen" {
				return true
			}
		}
	}
	return false
}

func ensureImageGenerationTool(body []byte, baseModel string, auth *cliproxyauth.Auth, headers http.Header) []byte {
	if util.IsCodexResponsesLiteRequest(body, headers) {
		return body
	}
	if strings.HasSuffix(baseModel, "spark") {
		return body
	}
	if isCodexFreePlanAuth(auth) {
		return body
	}

	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		body, _ = sjson.SetRawBytes(body, "tools", imageGenToolArrayJSON)
		return body
	}
	for _, t := range tools.Array() {
		if t.Get("type").String() == "image_generation" || isImageGenerationFunctionTool(t) {
			return body
		}
	}
	body, _ = sjson.SetRawBytes(body, "tools.-1", imageGenToolJSON)
	return body
}

func normalizeCodexParallelToolCalls(body []byte, headers http.Header) []byte {
	if util.IsCodexResponsesLiteRequest(body, headers) {
		body = helps.SetBoolIfDifferent(body, "parallel_tool_calls", false)
		return body
	}
	return normalizeCodexParallelToolCallsForTools(body)
}

func normalizeCodexParallelToolCallsForTools(body []byte) []byte {
	if !gjson.GetBytes(body, "parallel_tool_calls").Exists() {
		return body
	}

	tools := gjson.GetBytes(body, "tools")
	hasTools := tools.Exists() && tools.IsArray() && len(tools.Array()) > 0
	if hasTools {
		return body
	}

	body, _ = sjson.DeleteBytes(body, "parallel_tool_calls")
	return body
}

func publishCodexImageToolUsage(ctx context.Context, reporter *helps.UsageReporter, body []byte, completedData []byte) {
	detail, ok := helps.ParseCodexImageToolUsage(completedData)
	if !ok {
		return
	}
	reporter.EnsurePublished(ctx)
	reporter.PublishAdditionalModel(ctx, codexImageGenerationToolModel(body), detail)
}

func codexImageGenerationToolModel(body []byte) string {
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		for _, tool := range tools.Array() {
			if tool.Get("type").String() != "image_generation" {
				continue
			}
			if model := strings.TrimSpace(tool.Get("model").String()); model != "" {
				return model
			}
			break
		}
	}
	return codexDefaultImageToolModel
}
