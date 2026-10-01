package executor

import (
	"net/http"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// The image path posts to native Codex /responses, so Codex tool schemas keep
// their declared number types like every other native Codex path.
func TestCodexOpenAIImageBodyPreservesToolNumberSchemas(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","input":"draw","tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"yield_time_ms":{"type":"number"}}}}]}`)
	opts := cliproxyexecutor.Options{Headers: http.Header{"User-Agent": []string{"codex_cli_rs/0.155.0"}}}
	out, err := (&CodexExecutor{}).prepareCodexOpenAIImageBody(body, cliproxyexecutor.Request{Model: "gpt-image-2", Payload: body}, opts, "")
	if err != nil {
		t.Fatalf("prepare image body: %v", err)
	}
	if got := gjson.GetBytes(out, "tools.0.parameters.properties.yield_time_ms.type").String(); got != "number" {
		t.Fatalf("yield_time_ms.type = %q, want number on the native Codex image path; body=%s", got, out)
	}
}
