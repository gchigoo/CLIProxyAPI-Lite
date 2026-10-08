package helps

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestUsageReporterResponseModelSubstituted(t *testing.T) {
	cases := []struct {
		name     string
		model    string
		upstream string
		served   string
		want     bool
	}{
		{name: "case-only difference", model: "gpt-5.6-sol", served: "GPT-5.6-Sol", want: false},
		{name: "dated snapshot", model: "gpt-5.6-terra", served: "gpt-5.6-terra-2026-05-13", want: false},
		{name: "mapped upstream model", model: "kimi-k2.7-code", upstream: "kimi-for-coding", served: "kimi-for-coding", want: false},
		{name: "substituted model", model: "gpt-5.6-sol", served: "gpt-5.5-mini", want: true},
		{name: "mapped model substituted", model: "kimi-k2.7-code", upstream: "kimi-for-coding", served: "unexpected-model-v2", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			reporter := newCodexTestReporter(ctx, tc.model, nil)
			if tc.upstream != "" {
				reporter.SetUpstreamModel(tc.upstream)
			}
			reporter.SetResponseModel(tc.served)

			if got := reporter.ResponseModelSubstituted(); got != tc.want {
				t.Fatalf("ResponseModelSubstituted() = %t, want %t", got, tc.want)
			}
			record := reporter.buildRecord(usage.Detail{TotalTokens: 1}, false)
			if record.ResponseModel != tc.served || record.ResponseModelSubstituted != tc.want {
				t.Fatalf("record response model = %q substituted %t, want %q %t", record.ResponseModel, record.ResponseModelSubstituted, tc.served, tc.want)
			}
		})
	}
}

func TestUsageReporterResponseModelSubstitutedFalseWithoutServedModel(t *testing.T) {
	reporter := newCodexTestReporter(context.Background(), "gpt-5.6-sol", nil)
	if reporter.ResponseModelSubstituted() {
		t.Fatal("ResponseModelSubstituted() = true without a served model")
	}
	var nilReporter *UsageReporter
	if nilReporter.ResponseModelSubstituted() {
		t.Fatal("nil reporter reported a substitution")
	}
}
