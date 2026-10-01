package helps

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestTranslateRequestCompatibilityForExecutorToolIntegerTypes(t *testing.T) {
	const responsesPayload = `{"input":"hi","tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"yield_time_ms":{"type":"number"},"unrelated":{"type":"number"}}}}]}`
	const claudePayload = `{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"exec_command","input_schema":{"type":"object","properties":{"yield_time_ms":{"type":"number"},"unrelated":{"type":"number"}}}}]}`
	for _, route := range []struct {
		name       string
		from, to   sdktranslator.Format
		payload    string
		properties string
	}{
		{name: "responses_to_codex", from: sdktranslator.FormatOpenAIResponse, to: sdktranslator.FormatCodex, payload: responsesPayload, properties: "tools.0.parameters.properties"},
		{name: "claude_to_codex", from: sdktranslator.FormatClaude, to: sdktranslator.FormatCodex, payload: claudePayload, properties: "tools.0.parameters.properties"},
		{name: "responses_to_claude", from: sdktranslator.FormatOpenAIResponse, to: sdktranslator.FormatClaude, payload: responsesPayload, properties: "tools.0.input_schema.properties"},
		{name: "responses_passthrough", from: sdktranslator.FormatOpenAIResponse, to: sdktranslator.FormatOpenAIResponse, payload: responsesPayload, properties: "tools.0.parameters.properties"},
	} {
		for _, target := range []struct {
			name     string
			preserve bool
		}{
			{name: "codex", preserve: true},
			{name: "codex-websockets", preserve: true},
			{name: "xai"},
			{name: "antigravity"},
			{name: ""},
		} {
			for _, ua := range []string{"codex_cli_rs/0.1", "curl/8.7.1", ""} {
				for _, compat := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/target=%s/ua=%s/compat=%t", route.name, target.name, ua, compat), func(t *testing.T) {
						var headers http.Header
						if ua != "" {
							headers = http.Header{"User-Agent": []string{ua}, "X-Openai-Subagent": []string{"collab_spawn"}}
						}
						payload := []byte(route.payload)
						outputs := map[string][]byte{
							"body": TranslateRequestWithAPIKeyModelCompatibilityForExecutor(t.Context(), headers, &config.Config{}, target.name, route.from, route.to, "model", payload, false, compat),
						}
						if target.name == "" {
							outputs["legacy_body"] = TranslateRequestWithAPIKeyModelCompatibility(t.Context(), headers, &config.Config{}, route.from, route.to, "model", payload, false, compat)
						}
						wantType := "number"
						if ua == "codex_cli_rs/0.1" && !target.preserve {
							wantType = "integer"
						}
						for entry, body := range outputs {
							if got := gjson.GetBytes(body, route.properties+".yield_time_ms.type").String(); got != wantType {
								t.Errorf("%s: yield_time_ms.type = %q, want %q; body=%s", entry, got, wantType, body)
							}
							if got := gjson.GetBytes(body, route.properties+".unrelated.type").String(); got != "number" {
								t.Errorf("%s: unrelated.type = %q, want number", entry, got)
							}
						}
						if string(payload) != route.payload {
							t.Error("source payload was mutated")
						}
						if ua != "" && (headers.Get("User-Agent") != ua || headers.Get("X-Openai-Subagent") != "collab_spawn") {
							t.Error("request headers were mutated")
						}
					})
				}
			}
		}
	}
}

// Codex clients reach Gemini through Antigravity on the production deployment;
// the translated Gemini declarations must carry integer types.
func TestTranslateRequestPairToAntigravityNormalizesCodexToolIntegerTypes(t *testing.T) {
	payload := []byte(`{"model":"gemini-3.8-flash-high","input":"hi","tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"yield_time_ms":{"type":"number"},"cmd":{"type":"string"}}}}]}`)
	headers := http.Header{"User-Agent": []string{"codex_cli_rs/0.155.0"}}
	_, working := TranslateRequestPairWithCodexMultiAgentV2(t.Context(), headers, &config.Config{}, sdktranslator.FormatOpenAIResponse, sdktranslator.FormatAntigravity, "gemini-3.8-flash-high", payload, payload, false)
	declaration := gjson.GetBytes(working, "request.tools.0.functionDeclarations.0")
	schema := declaration.Get("parametersJsonSchema")
	if !schema.Exists() {
		schema = declaration.Get("parameters")
	}
	if got := schema.Get("properties.yield_time_ms.type").String(); got != "integer" {
		t.Fatalf("yield_time_ms.type = %q, want integer; body=%s", got, working)
	}
	if got := schema.Get("properties.cmd.type").String(); got != "string" {
		t.Fatalf("cmd.type = %q, want string; body=%s", got, working)
	}
}
