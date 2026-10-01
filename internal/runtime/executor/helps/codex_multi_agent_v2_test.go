package helps

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func geminiToolHistoryPayload(turns int) []byte {
	contents := []string{`{"role":"user","parts":[{"text":"start"}]}`}
	for i := 0; i < turns; i++ {
		contents = append(contents,
			fmt.Sprintf(`{"role":"user","parts":[{"text":"ask %d"}]}`, i),
			fmt.Sprintf(`{"role":"model","parts":[{"text":"think %d"},{"thoughtSignature":"sig-%d","functionCall":{"id":"c%d","name":"read_file","args":{"path":"a%d.go"}}}]}`, i, i, i, i),
			fmt.Sprintf(`{"role":"user","parts":[{"functionResponse":{"id":"c%d","name":"read_file","response":{"content":"data %d"}}}]}`, i, i),
			fmt.Sprintf(`{"role":"model","parts":[{"text":"answer %d"}]}`, i))
	}
	return []byte(fmt.Sprintf(
		`{"contents":[%s],"tools":[{"functionDeclarations":[{"name":"read_file","description":"read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]}],"generationConfig":{"temperature":1}}`,
		strings.Join(contents, ",")))
}

// TestTranslateRequestPairMatchesSeparateTranslations pins the reuse fast path to
// the behavior of translating both payloads independently.
func TestTranslateRequestPairMatchesSeparateTranslations(t *testing.T) {
	from := sdktranslator.FormatGemini
	to := sdktranslator.FromString("antigravity")
	cfg := &config.Config{}
	const model = "gemini-3.6-flash-high"

	for _, turns := range []int{0, 1, 5, 20} {
		payload := geminiToolHistoryPayload(turns)
		// Same bytes in a different backing array forces the translate-twice branch.
		detached := append([]byte(nil), payload...)

		want := TranslateRequestWithCodexMultiAgentV2(context.Background(), http.Header{}, cfg, from, to, model, payload, true)

		reuseBase, reuseWork := TranslateRequestPairWithCodexMultiAgentV2(
			context.Background(), http.Header{}, cfg, from, to, model, payload, payload, true)
		twiceBase, twiceWork := TranslateRequestPairWithCodexMultiAgentV2(
			context.Background(), http.Header{}, cfg, from, to, model, payload, detached, true)

		for name, got := range map[string][]byte{
			"reuse baseline": reuseBase,
			"reuse working":  reuseWork,
			"twice baseline": twiceBase,
			"twice working":  twiceWork,
		} {
			if !bytes.Equal(want, got) {
				t.Fatalf("turns=%d: %s translation differs from a standalone translation", turns, name)
			}
		}

		if len(reuseBase) > 0 && &reuseBase[0] == &reuseWork[0] {
			t.Fatalf("turns=%d: working copy aliases the baseline; later in-place edits would corrupt it", turns)
		}

		// The caller mutates the working copy, so the baseline must stay intact.
		baselineBefore := append([]byte(nil), reuseBase...)
		reuseWork[0] = 'X'
		if !bytes.Equal(baselineBefore, reuseBase) {
			t.Fatalf("turns=%d: mutating the working copy changed the baseline", turns)
		}
	}
}

// TestTranslateRequestPairTranslatesDistinctPayloads guards the case where the
// executor really does hand over two different requests.
func TestTranslateRequestPairTranslatesDistinctPayloads(t *testing.T) {
	from := sdktranslator.FormatGemini
	to := sdktranslator.FromString("antigravity")
	cfg := &config.Config{}
	const model = "gemini-3.6-flash-high"

	original := geminiToolHistoryPayload(2)
	request := geminiToolHistoryPayload(4)

	base, work := TranslateRequestPairWithCodexMultiAgentV2(
		context.Background(), http.Header{}, cfg, from, to, model, original, request, true)

	wantBase := TranslateRequestWithCodexMultiAgentV2(context.Background(), http.Header{}, cfg, from, to, model, original, true)
	wantWork := TranslateRequestWithCodexMultiAgentV2(context.Background(), http.Header{}, cfg, from, to, model, request, true)

	if !bytes.Equal(wantBase, base) {
		t.Fatal("baseline translation differs for distinct payloads")
	}
	if !bytes.Equal(wantWork, work) {
		t.Fatal("working translation differs for distinct payloads")
	}
	if bytes.Equal(base, work) {
		t.Fatal("distinct payloads produced identical translations; the reuse path was taken by mistake")
	}
}

func TestSameByteSlice(t *testing.T) {
	buf := []byte("payload")
	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{"identical slice", buf, buf, true},
		{"same array same length", buf[:3], buf[:3], true},
		{"equal bytes different array", buf, append([]byte(nil), buf...), false},
		{"different length", buf, buf[:3], false},
		{"both nil", nil, nil, true},
		{"nil and empty", nil, []byte{}, true},
		{"nil and non-empty", nil, buf, false},
		{"offset alias", buf, buf[1:], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameByteSlice(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameByteSlice() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTranslateRequestWithCodexMultiAgentV2_NormalizesCodexToolTypes(t *testing.T) {
	payload := []byte(`{
		"model": "gpt-5.5",
		"input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools": [
			{
				"type": "function",
				"name": "exec_command",
				"parameters": {
					"type": "object",
					"properties": {
						"yield_time_ms": {"type": "number"},
						"timeout_ms": {"type": "number"}
					}
				}
			}
		]
	}`)

	headers := http.Header{"User-Agent": []string{"codex-tui/0.154.0"}}
	out := TranslateRequestWithCodexMultiAgentV2(
		context.Background(),
		headers,
		&config.Config{},
		sdktranslator.FormatOpenAIResponse,
		sdktranslator.FormatClaude,
		"claude-opus-5-5",
		payload,
		false,
	)

	// In Claude format, tool is under tools[0].input_schema.properties
	if got := gjson.GetBytes(out, "tools.0.input_schema.properties.yield_time_ms.type").String(); got != "integer" {
		t.Errorf("translated Claude tool yield_time_ms type = %q, want integer; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.input_schema.properties.timeout_ms.type").String(); got != "integer" {
		t.Errorf("translated Claude tool timeout_ms type = %q, want integer; out=%s", got, out)
	}

	outGemini := TranslateRequestWithCodexMultiAgentV2(
		context.Background(),
		headers,
		&config.Config{},
		sdktranslator.FormatOpenAIResponse,
		sdktranslator.FormatGemini,
		"gemini-2.5-flash",
		payload,
		false,
	)

	// In Gemini format, tool is under functionDeclarations with parameters or parametersJsonSchema
	geminiParam := gjson.GetBytes(outGemini, "tools.0.functionDeclarations.0.parametersJsonSchema.properties")
	if !geminiParam.Exists() {
		geminiParam = gjson.GetBytes(outGemini, "tools.0.function_declarations.0.parameters.properties")
	}
	if got := geminiParam.Get("yield_time_ms.type").String(); got != "integer" {
		t.Errorf("translated Gemini tool yield_time_ms type = %q, want integer; out=%s", got, outGemini)
	}
	if got := geminiParam.Get("timeout_ms.type").String(); got != "integer" {
		t.Errorf("translated Gemini tool timeout_ms type = %q, want integer; out=%s", got, outGemini)
	}
}
