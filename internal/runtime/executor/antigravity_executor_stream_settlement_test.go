package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func executeAntigravityTestStream(t *testing.T, ctx context.Context, authID, baseURL string, format sdktranslator.Format, payload string) <-chan cliproxyexecutor.StreamChunk {
	t.Helper()
	executor := NewAntigravityExecutor(&config.Config{
		Antigravity:  config.AntigravityConfig{},
		RequestRetry: 1,
	})
	result, errExecute := executor.ExecuteStream(ctx, &cliproxyauth.Auth{
		ID: authID,
		Metadata: map[string]any{
			"access_token": "token-123",
			"expired":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			"project_id":   "project-1",
		},
		Attributes: map[string]string{"base_url": baseURL},
	}, cliproxyexecutor.Request{
		Model:   "gemini-3.7-flash",
		Payload: []byte(payload),
	}, cliproxyexecutor.Options{
		SourceFormat:   format,
		ResponseFormat: format,
		Stream:         true,
	})
	if errExecute != nil {
		t.Fatalf("ExecuteStream() error = %v", errExecute)
	}
	return result.Chunks
}

// A stop chunk without usage is followed by a separate usage frame. A client
// that leaves between the two still completed the turn and must be recorded.
func TestAntigravityStreamDisconnectBeforeSplitUsagePublishesRecord(t *testing.T) {
	const authID = "antigravity-split-usage-disconnect-test"
	capture := &antigravityUsageCapture{authID: authID, records: make(chan usage.Record, 4)}
	usage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() {
		usage.RegisterNamedPlugin(t.Name(), antigravityUsageNoop{})
	})

	const stopWithoutUsage = `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"modelVersion":"gemini-3.7-flash","responseId":"resp-split","traceId":"trace-split-disconnect"}}

`
	upstreamClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stopWithoutUsage)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-upstreamClosed:
		}
	}))
	defer func() {
		close(upstreamClosed)
		server.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chunks := executeAntigravityTestStream(t, ctx, authID, server.URL, sdktranslator.FormatOpenAIResponse,
		`{"model":"gemini-3.7-flash","stream":true,"input":"hello"}`)

	terminalReceived := false
	for chunk := range chunks {
		if chunk.Err != nil {
			break
		}
		if gjson.GetBytes(helpsJSONPayloadForTest(chunk.Payload), "type").String() == "response.completed" {
			terminalReceived = true
			cancel()
			break
		}
	}
	if !terminalReceived {
		t.Fatal("expected response.completed before cancellation")
	}

	select {
	case record := <-capture.records:
		if record.Failed {
			t.Fatalf("usage record marked failed after a delivered terminal chunk: status=%d body=%q", record.Fail.StatusCode, record.Fail.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for usage record; the delivered turn was never recorded")
	}
}

// A frame that never becomes valid JSON must not swallow later SSE frames.
func TestAntigravityStreamMalformedFrameDoesNotSwallowLaterFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"text":"lost

data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"after malformed frame"}]},"finishReason":"STOP"}],"modelVersion":"gemini-3.7-flash","usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7}}}

`)
	}))
	defer server.Close()

	chunks := collectAntigravityStream(t, server.URL, sdktranslator.FormatOpenAI, sdktranslator.FormatOpenAI,
		`{"model":"gemini-3.7-flash","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	joined := bytes.Join(chunks, []byte("\n"))
	if !bytes.Contains(joined, []byte("after malformed frame")) {
		t.Fatalf("frame after the malformed payload was not forwarded: %s", joined)
	}
	if !bytes.Contains(joined, []byte(`"finish_reason":"stop"`)) {
		t.Fatalf("stream did not complete after the malformed payload: %s", joined)
	}
}

// An incomplete JSON payload at end of stream is a truncated response, not a completion.
func TestAntigravityStreamIncompletePayloadAtEOFReportsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"modelVersion":"gemini-3.7-flash"}}

{
  "error": {
    "code": 503,
`)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chunks := executeAntigravityTestStream(t, ctx, "antigravity-incomplete-eof-test", server.URL, sdktranslator.FormatOpenAI,
		`{"model":"gemini-3.7-flash","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	var streamErr error
	for chunk := range chunks {
		if chunk.Err != nil {
			streamErr = chunk.Err
			continue
		}
		if bytes.Contains(chunk.Payload, []byte(`"finish_reason":"stop"`)) {
			t.Fatalf("incomplete payload became a successful completion: %s", chunk.Payload)
		}
	}
	if streamErr == nil {
		t.Fatal("expected a stream error for the incomplete payload")
	}
	statusError, ok := streamErr.(interface{ StatusCode() int })
	if !ok || statusError.StatusCode() != http.StatusBadGateway {
		t.Fatalf("incomplete payload error = %v, want status %d", streamErr, http.StatusBadGateway)
	}
}

func helpsJSONPayloadForTest(chunk []byte) []byte {
	for _, line := range bytes.Split(chunk, []byte("\n")) {
		if payload := helps.JSONPayload(line); len(payload) > 0 {
			return payload
		}
	}
	return chunk
}
