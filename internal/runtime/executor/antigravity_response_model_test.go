package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// Lite publishes Antigravity stream usage from the terminal frame, so the
// served model must be observed before that publish, including when usage
// arrives in a separate frame after the stop chunk.
func TestAntigravityStreamUsageRecordCarriesResponseModel(t *testing.T) {
	cases := []struct {
		name string
		sse  string
	}{
		{
			name: "terminal frame carries usage",
			sse: `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"modelVersion":"gemini-3.7-flash-lite","usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}}

`,
		},
		{
			name: "usage arrives after the stop chunk",
			sse: `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"modelVersion":"gemini-3.7-flash-lite","traceId":"trace-response-model-split"}}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5},"traceId":"trace-response-model-split"}}

`,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authID := "antigravity-response-model-" + string(rune('a'+i))
			capture := &antigravityUsageCapture{authID: authID, records: make(chan usage.Record, 4)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() {
				usage.RegisterNamedPlugin(t.Name(), antigravityUsageNoop{})
			})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.sse)
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for chunk := range executeAntigravityTestStream(t, ctx, authID, server.URL, sdktranslator.FormatOpenAIResponse,
				`{"model":"gemini-3.7-flash","stream":true,"input":"hello"}`) {
				if chunk.Err != nil {
					t.Fatalf("unexpected stream error: %v", chunk.Err)
				}
			}

			select {
			case record := <-capture.records:
				if record.ResponseModel != "gemini-3.7-flash-lite" {
					t.Fatalf("record.ResponseModel = %q, want gemini-3.7-flash-lite", record.ResponseModel)
				}
				if record.Model != "gemini-3.7-flash" {
					t.Fatalf("record.Model = %q, want gemini-3.7-flash", record.Model)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for usage record")
			}
		})
	}
}

func TestAntigravityExecuteUsageRecordCarriesResponseModel(t *testing.T) {
	const authID = "antigravity-response-model-nonstream"
	capture := &antigravityUsageCapture{authID: authID, records: make(chan usage.Record, 4)}
	usage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() {
		usage.RegisterNamedPlugin(t.Name(), antigravityUsageNoop{})
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"modelVersion":"gemini-3.7-flash-lite","usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}}`)
	}))
	defer server.Close()

	executor := NewAntigravityExecutor(&config.Config{
		Antigravity:  config.AntigravityConfig{},
		RequestRetry: 1,
	})
	_, errExecute := executor.Execute(context.Background(), &cliproxyauth.Auth{
		ID: authID,
		Metadata: map[string]any{
			"access_token": "token-123",
			"expired":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			"project_id":   "project-1",
		},
		Attributes: map[string]string{"base_url": server.URL},
	}, cliproxyexecutor.Request{
		Model:   "gemini-3.7-flash",
		Payload: []byte(`{"model":"gemini-3.7-flash","messages":[{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FormatOpenAI,
		ResponseFormat: sdktranslator.FormatOpenAI,
	})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}

	select {
	case record := <-capture.records:
		if record.ResponseModel != "gemini-3.7-flash-lite" {
			t.Fatalf("record.ResponseModel = %q, want gemini-3.7-flash-lite", record.ResponseModel)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for usage record")
	}
}
