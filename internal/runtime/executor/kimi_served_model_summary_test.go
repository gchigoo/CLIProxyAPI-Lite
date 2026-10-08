package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/servedmodel"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// Kimi keeps the client model in Record.Model and sends a mapped model
// upstream. The served-model summary must judge substitution against the
// mapped model, exactly like the substitution warning.
func TestKimiServedModelSummaryJudgesSubstitutionAgainstMappedModel(t *testing.T) {
	cases := []struct {
		name            string
		requested       string
		served          string
		wantSubstituted bool
	}{
		{name: "mapped model served", requested: "kimi-k2.7-code", served: "kimi-for-coding", wantSubstituted: false},
		{name: "unexpected model served", requested: "kimi-k2.7-code", served: "unexpected-model-v2", wantSubstituted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", kimiRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(
						`{"id":"chatcmpl-1","object":"chat.completion","model":"` + tc.served + `","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"total_tokens":10}}`,
					)),
				}, nil
			}))

			alias := "kimi-served-summary-" + strings.ReplaceAll(tc.name, " ", "-")
			capture := &multiProviderUsageCapture{alias: alias, records: make(chan coreusage.Record, 4)}
			coreusage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() {
				coreusage.RegisterNamedPlugin(t.Name(), multiProviderNoopUsagePlugin{})
			})

			executor := NewKimiExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{
				ID:         "kimi-served-summary-auth",
				Provider:   "kimi",
				Attributes: map[string]string{},
				Metadata:   map[string]any{"access_token": "test-key"},
			}
			ctx = coreusage.WithRequestedModelAlias(ctx, alias)
			if _, err := executor.Execute(ctx, auth, cliproxyexecutor.Request{
				Model:   tc.requested,
				Payload: []byte(`{"model":"` + tc.requested + `","messages":[{"role":"user","content":"hello"}]}`),
			}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI}); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			record := capture.await(t)
			tracker := servedmodel.NewTracker(nil)
			tracker.HandleUsage(context.Background(), record)
			entries := tracker.Snapshot(record.AuthID)
			if len(entries) != 1 {
				t.Fatalf("Snapshot() = %+v, want one entry", entries)
			}
			if entries[0].ServedModel != tc.served || entries[0].Substituted != tc.wantSubstituted {
				t.Fatalf("summary entry = %+v, want served %q substituted %t", entries[0], tc.served, tc.wantSubstituted)
			}
		})
	}
}
