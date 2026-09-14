package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/sjson"

	. "github.com/router-for-me/CLIProxyAPI/v7/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
)

func preferExecutionProvider(providers []string, preferred string) []string {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred == "" || len(providers) < 2 {
		return providers
	}
	preferredIndex := -1
	for i := range providers {
		if strings.ToLower(strings.TrimSpace(providers[i])) == preferred {
			preferredIndex = i
			break
		}
	}
	if preferredIndex <= 0 {
		return providers
	}
	out := make([]string, 0, len(providers))
	out = append(out, providers[preferredIndex])
	out = append(out, providers[:preferredIndex]...)
	out = append(out, providers[preferredIndex+1:]...)
	return out
}

func adjustExecutionProvidersForEntryProtocol(entryProtocol string, providers []string) []string {
	if entryProtocol == Interactions {
		return preferExecutionProvider(providers, GeminiInteractions)
	}
	if supportsNativeInteractionsEntryProtocol(entryProtocol) {
		return providers
	}
	return excludeExecutionProvider(providers, GeminiInteractions)
}

func supportsNativeInteractionsEntryProtocol(entryProtocol string) bool {
	switch entryProtocol {
	case Interactions, OpenAI, OpenaiResponse, Claude, Gemini:
		return true
	default:
		return false
	}
}

func excludeExecutionProvider(providers []string, excluded string) []string {
	excluded = strings.ToLower(strings.TrimSpace(excluded))
	if excluded == "" || len(providers) == 0 {
		return providers
	}
	excludedIndex := -1
	for i := range providers {
		if strings.ToLower(strings.TrimSpace(providers[i])) == excluded {
			excludedIndex = i
			break
		}
	}
	if excludedIndex == -1 {
		return providers
	}
	out := make([]string, 0, len(providers)-1)
	out = append(out, providers[:excludedIndex]...)
	out = append(out, providers[excludedIndex+1:]...)
	return out
}

func (h *BaseAPIHandler) getRequestDetails(modelName string) (providers []string, normalizedModel string, err *interfaces.ErrorMessage) {
	return h.getRequestDetailsWithOptions(modelName, false)
}

// providersForExecution resolves the providers and normalized model for a request.
func (h *BaseAPIHandler) providersForExecution(modelName, originalRequestedModel string, allowImageModel bool, execOptions modelExecutionOptions) ([]string, string, *interfaces.ErrorMessage) {
	forcedProvider := strings.ToLower(strings.TrimSpace(execOptions.ForcedProvider))
	if forcedProvider != "" {
		normalizedModel := strings.TrimSpace(modelName)
		if normalizedModel == "" {
			normalizedModel = strings.TrimSpace(originalRequestedModel)
		}
		if errMsg := h.validateImageOnlyModel(normalizedModel, allowImageModel); errMsg != nil {
			return nil, "", errMsg
		}
		return []string{forcedProvider}, normalizedModel, nil
	}
	return h.getRequestDetailsWithOptions(modelName, allowImageModel)
}

func (h *BaseAPIHandler) getRequestDetailsWithOptions(modelName string, allowImageModel bool) (providers []string, normalizedModel string, err *interfaces.ErrorMessage) {
	resolvedModelName := modelName
	initialSuffix := thinking.ParseSuffix(modelName)
	if initialSuffix.ModelName == "auto" {
		if h != nil && h.AuthManager != nil && h.AuthManager.HomeEnabled() {
			resolvedModelName = modelName
		} else {
			resolvedBase := util.ResolveAutoModel(initialSuffix.ModelName)
			if initialSuffix.HasSuffix {
				resolvedModelName = fmt.Sprintf("%s(%s)", resolvedBase, initialSuffix.RawSuffix)
			} else {
				resolvedModelName = resolvedBase
			}
		}
	} else {
		if h != nil && h.AuthManager != nil && h.AuthManager.HomeEnabled() {
			resolvedModelName = modelName
		} else {
			resolvedModelName = util.ResolveAutoModel(modelName)
		}
	}

	parsed := thinking.ParseSuffix(resolvedModelName)
	baseModel := strings.TrimSpace(parsed.ModelName)

	if errMsg := h.validateImageOnlyModel(baseModel, allowImageModel); errMsg != nil {
		return nil, "", errMsg
	}

	if h != nil && h.AuthManager != nil && h.AuthManager.HomeEnabled() {
		return []string{"home"}, resolvedModelName, nil
	}

	providers = util.GetProviderName(baseModel)
	// Fallback: if baseModel has no provider but differs from resolvedModelName,
	// try using the full model name. This handles edge cases where custom models
	// may be registered with their full suffixed name (e.g., "my-model(8192)").
	// Evaluated in Story 11.8: This fallback is intentionally preserved to support
	// custom model registrations that include thinking suffixes.
	if len(providers) == 0 && baseModel != resolvedModelName {
		providers = util.GetProviderName(resolvedModelName)
	}

	if len(providers) == 0 {
		// The client asked for a model this proxy cannot route. Report it as a request
		// error so streaming clients receive an actionable message instead of a
		// gateway failure they would keep retrying. 400 is used rather than 404 to keep
		// it distinguishable from an unregistered HTTP route.
		// The model name is client supplied, so it is inserted through sjson rather
		// than formatted into the JSON literal: an unescaped quote would otherwise
		// corrupt the body or let the caller overwrite the error code.
		body := `{"error":{"message":"","type":"invalid_request_error","code":"model_not_found","param":"model"}}`
		body, errSet := sjson.Set(body, "error.message", "unknown provider for model "+modelName)
		if errSet != nil {
			body = `{"error":{"message":"unknown provider for model","type":"invalid_request_error","code":"model_not_found","param":"model"}}`
		}
		return nil, "", &interfaces.ErrorMessage{
			StatusCode: http.StatusBadRequest,
			Error:      errors.New(body),
		}
	}

	// The thinking suffix is preserved in the model name itself, so no
	// metadata-based configuration passing is needed.
	return providers, resolvedModelName, nil
}

func (h *BaseAPIHandler) validateImageOnlyModel(modelName string, allowImageModel bool) *interfaces.ErrorMessage {
	baseModel := strings.TrimSpace(thinking.ParseSuffix(modelName).ModelName)
	if baseModel == "" {
		baseModel = strings.TrimSpace(modelName)
	}
	if isOpenAIImageOnlyModel(baseModel) && !allowImageModel {
		return &interfaces.ErrorMessage{
			StatusCode: http.StatusServiceUnavailable,
			Error:      fmt.Errorf("model %s is only supported on /v1/images/generations and /v1/images/edits", routeModelBaseName(baseModel)),
		}
	}
	return nil
}

func isOpenAIImageOnlyModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(routeModelBaseName(model))) {
	case "gpt-image-1.5", "gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2.5", "grok-imagine-image", "grok-imagine-image-quality", "grok-imagine-image-2.0":
		return true
	default:
		return false
	}
}

func routeModelBaseName(model string) string {
	model = strings.TrimSpace(model)
	if idx := strings.LastIndex(model, "/"); idx >= 0 && idx < len(model)-1 {
		return strings.TrimSpace(model[idx+1:])
	}
	return model
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
