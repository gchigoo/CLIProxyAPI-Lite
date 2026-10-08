package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// The Meta executor must observe the model Meta reports before it publishes
// usage, on both the non-stream and the stream path (CUSTOMIZATIONS.md item 9).
func TestMetaExecutorUsageRecordCarriesServedModel(t *testing.T) {
	cases := []struct {
		served      string
		substituted bool
	}{
		{served: "muse-spark-1.3-served", substituted: true},
		{served: "muse-spark-1.3", substituted: false},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.served, stream), func(t *testing.T) {
				sse := fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":%q}}\n\n"+
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":%q,\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":20,\"total_tokens\":30}}}\n\n",
					tc.served, tc.served)
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
				executor := NewMetaExecutor(&config.Config{})
				auth := &cliproxyauth.Auth{
					ID:         "meta-served.json",
					Provider:   "meta",
					Attributes: map[string]string{"base_url": server.URL, "api_key": "LLM|test"},
					Metadata:   map[string]any{"type": "meta"},
				}
				req := cliproxyexecutor.Request{
					Model:   "muse-spark-1.3",
					Payload: []byte(`{"model":"muse-spark-1.3","messages":[{"role":"user","content":"hello"}]}`),
				}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI, Stream: stream}
				if stream {
					result, errStream := executor.ExecuteStream(ctx, auth, req, opts)
					if errStream != nil {
						t.Fatalf("ExecuteStream() error = %v", errStream)
					}
					for range result.Chunks {
					}
				} else if _, errExecute := executor.Execute(ctx, auth, req, opts); errExecute != nil {
					t.Fatalf("Execute() error = %v", errExecute)
				}

				record := capture.await(t)
				if record.ResponseModel != tc.served || record.ResponseModelSubstituted != tc.substituted {
					t.Fatalf("record response model = %q substituted %t, want %q %t", record.ResponseModel, record.ResponseModelSubstituted, tc.served, tc.substituted)
				}
			})
		}
	}
}
