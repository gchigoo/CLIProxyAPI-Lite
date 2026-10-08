package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// An invalid explicit proxy must fail the request-time mint instead of
// sending the DCA token over a direct connection.
func TestMetaExecutorRefreshFailsClosedOnInvalidProxy(t *testing.T) {
	var mintCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mintCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_key":"LLM|direct","base_url":"https://api.meta.ai/v1"}`))
	}))
	defer server.Close()
	t.Setenv("META_MINT_URL", server.URL)

	auth := &cliproxyauth.Auth{
		ID:       "meta-proxy.json",
		Provider: "meta",
		ProxyURL: "socks5://%zz-invalid",
		Metadata: map[string]any{"type": "meta", "dca_token": "dca:secret"},
	}
	_, err := NewMetaExecutor(&config.Config{}).Refresh(context.Background(), auth)
	if err == nil {
		t.Fatal("Refresh() error = nil, want proxy error")
	}
	if got := mintCalls.Load(); got != 0 {
		t.Fatalf("mint endpoint reached directly %d times with an invalid explicit proxy", got)
	}
	if strings.Contains(err.Error(), "dca:secret") {
		t.Fatalf("error leaks the DCA token: %v", err)
	}
}

// A Meta stream that closes before response.completed or response.incomplete
// must surface an error and a failed usage record, not a clean finish.
func TestMetaExecuteStreamFailsWhenStreamEndsBeforeCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"muse-spark-1.3\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
	}))
	defer server.Close()

	alias := t.Name()
	capture := &codexResponseModelUsageCapture{alias: alias, records: make(chan coreusage.Record, 4)}
	coreusage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() {
		coreusage.RegisterNamedPlugin(t.Name(), codexResponseModelNoopUsagePlugin{})
	})

	ctx := coreusage.WithRequestedModelAlias(context.Background(), alias)
	auth := &cliproxyauth.Auth{
		ID:         "meta-truncated.json",
		Provider:   "meta",
		Attributes: map[string]string{"base_url": server.URL, "api_key": "LLM|test"},
		Metadata:   map[string]any{"type": "meta"},
	}
	req := cliproxyexecutor.Request{
		Model:   "muse-spark-1.3",
		Payload: []byte(`{"model":"muse-spark-1.3","messages":[{"role":"user","content":"hello"}]}`),
	}
	result, errStream := NewMetaExecutor(&config.Config{}).ExecuteStream(ctx, auth, req, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI, Stream: true})
	if errStream != nil {
		t.Fatalf("ExecuteStream() error = %v", errStream)
	}
	var streamErr error
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if streamErr == nil {
		t.Fatal("truncated stream finished without an error chunk")
	}
	var coded interface{ StatusCode() int }
	if !errors.As(streamErr, &coded) || coded.StatusCode() != http.StatusRequestTimeout {
		t.Fatalf("stream error = %v, want status %d", streamErr, http.StatusRequestTimeout)
	}
	if record := capture.await(t); !record.Failed {
		t.Fatalf("usage record Failed = false for a truncated stream: %#v", record)
	}
}
