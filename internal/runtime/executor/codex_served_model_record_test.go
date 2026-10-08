package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// Lite's Codex executor builds requests through the personal native identity
// path; the published usage record must still carry the served model and the
// substitution flag on both the non-stream and the stream path.
func TestCodexExecutorUsageRecordCarriesServedModel(t *testing.T) {
	const sse = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.5-mini\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-5.5-mini\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":20,\"total_tokens\":30}}}\n\n"

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, sse)
			}))
			t.Cleanup(server.Close)

			alias := t.Name()
			capture := &codexResponseModelUsageCapture{alias: alias, records: make(chan coreusage.Record, 4)}
			coreusage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() {
				coreusage.RegisterNamedPlugin(t.Name(), codexResponseModelNoopUsagePlugin{})
			})

			ctx := coreusage.WithRequestedModelAlias(context.Background(), alias)
			executor := NewCodexExecutor(&config.Config{})
			req := cliproxyexecutor.Request{
				Model:   "gpt-5.6-sol",
				Payload: []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}]}`),
			}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI, Stream: stream}
			if stream {
				result, errStream := executor.ExecuteStream(ctx, codexOAuthTestAuth(server.URL), req, opts)
				if errStream != nil {
					t.Fatalf("ExecuteStream() error = %v", errStream)
				}
				for range result.Chunks {
				}
			} else if _, errExecute := executor.Execute(ctx, codexOAuthTestAuth(server.URL), req, opts); errExecute != nil {
				t.Fatalf("Execute() error = %v", errExecute)
			}

			record := capture.await(t)
			if record.ResponseModel != "gpt-5.5-mini" || !record.ResponseModelSubstituted {
				t.Fatalf("record response model = %q substituted %t, want gpt-5.5-mini true", record.ResponseModel, record.ResponseModelSubstituted)
			}
		})
	}
}
