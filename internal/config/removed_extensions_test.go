package config

import (
	"testing"
)

func TestParseConfigBytes_LegacyPluginsSectionIsInert(t *testing.T) {
	yamlContent := []byte(`
port: 8080
auth-dir: "~/.cli-proxy-api"
plugins:
  enabled: true
  dir: "/custom/plugins/path"
  store-sources:
    - "https://example.com/store.json"
  configs:
    sample:
      enabled: true
      priority: 10
api-keys:
  - "sk-test-key"
`)

	cfg, err := ParseConfigBytes(yamlContent)
	if err != nil {
		t.Fatalf("ParseConfigBytes failed on legacy plugins YAML: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Port != 8080 {
		t.Fatalf("cfg.Port = %d, want 8080", cfg.Port)
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != "sk-test-key" {
		t.Fatalf("cfg.APIKeys = %#v, want [sk-test-key]", cfg.APIKeys)
	}
}
