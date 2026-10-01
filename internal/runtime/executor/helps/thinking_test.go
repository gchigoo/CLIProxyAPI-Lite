package helps_test

import (
	"testing"

	helps "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/thinking/provider/gemini"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestApplyThinkingWithSourcePayloadPreservesOriginalOnlySummary(t *testing.T) {
	currentSource := []byte(`{"model":"gemini-3.6-flash","input":"hi"}`)
	originalSource := []byte(`{"model":"gemini-3.6-flash","reasoning":{"summary":null},"input":"hi"}`)
	body := []byte(`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`)

	out, err := helps.ApplyThinkingWithSourcePayload(
		body,
		currentSource,
		originalSource,
		"gemini-3.6-flash",
		sdktranslator.FormatOpenAIResponse.String(),
		sdktranslator.FormatGemini.String(),
		"gemini",
	)
	if err != nil {
		t.Fatalf("ApplyThinkingWithSourcePayload() error = %v", err)
	}
	if include := gjson.GetBytes(out, "generationConfig.thinkingConfig.includeThoughts"); !include.Exists() || include.Bool() {
		t.Fatalf("original disabled summary was not preserved: %s", out)
	}
}

func TestApplyThinkingWithSourcePayload_AntigravityResponsesReasoningSummaryAuto(t *testing.T) {
	source := []byte(`{"model":"gemini-3.8-flash-high","reasoning":{"effort":"low","summary":"auto"},"input":"hi"}`)
	translated := sdktranslator.TranslateRequest(
		sdktranslator.FormatOpenAIResponse,
		sdktranslator.FormatAntigravity,
		"gemini-3.8-flash-high",
		source,
		false,
	)

	out, err := helps.ApplyThinkingWithSourcePayload(
		translated,
		source,
		source,
		"gemini-3.8-flash-high",
		sdktranslator.FormatOpenAIResponse.String(),
		sdktranslator.FormatAntigravity.String(),
		"antigravity",
	)
	if err != nil {
		t.Fatalf("ApplyThinkingWithSourcePayload() error = %v", err)
	}
	include := gjson.GetBytes(out, "request.generationConfig.thinkingConfig.includeThoughts")
	if !include.Exists() || !include.Bool() {
		t.Fatalf("includeThoughts not enabled: %s", out)
	}
	level := gjson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel").String()
	if level != "low" {
		t.Fatalf("thinkingLevel = %q, want low: %s", level, out)
	}
}
