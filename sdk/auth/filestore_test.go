package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestExtractAccessToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata map[string]any
		expected string
	}{
		{
			"antigravity top-level access_token",
			map[string]any{"access_token": "tok-abc"},
			"tok-abc",
		},
		{
			"gemini nested token.access_token",
			map[string]any{
				"token": map[string]any{"access_token": "tok-nested"},
			},
			"tok-nested",
		},
		{
			"top-level takes precedence over nested",
			map[string]any{
				"access_token": "tok-top",
				"token":        map[string]any{"access_token": "tok-nested"},
			},
			"tok-top",
		},
		{
			"empty metadata",
			map[string]any{},
			"",
		},
		{
			"whitespace-only access_token",
			map[string]any{"access_token": "   "},
			"",
		},
		{
			"wrong type access_token",
			map[string]any{"access_token": 12345},
			"",
		},
		{
			"token is not a map",
			map[string]any{"token": "not-a-map"},
			"",
		},
		{
			"nested whitespace-only",
			map[string]any{
				"token": map[string]any{"access_token": "  "},
			},
			"",
		},
		{
			"fallback to nested when top-level empty",
			map[string]any{
				"access_token": "",
				"token":        map[string]any{"access_token": "tok-fallback"},
			},
			"tok-fallback",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractAccessToken(tt.metadata)
			if got != tt.expected {
				t.Errorf("extractAccessToken() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFileTokenStoreSaveExistingMetadataSetsFileAttributes(t *testing.T) {
	tests := []struct {
		name          string
		existingToken string
		savedToken    string
	}{
		{name: "unchanged content", existingToken: "token", savedToken: "token"},
		{name: "overwritten content", existingToken: "old-token", savedToken: "new-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := t.TempDir()
			fileName := "antigravity-user.json"
			path := filepath.Join(baseDir, fileName)
			existing := []byte(`{"type":"antigravity","access_token":"` + tt.existingToken + `","disabled":false}`)
			if errWrite := os.WriteFile(path, existing, 0o600); errWrite != nil {
				t.Fatalf("write existing auth file: %v", errWrite)
			}

			store := NewFileTokenStore()
			store.SetBaseDir(baseDir)
			auth := &cliproxyauth.Auth{
				ID:       fileName,
				FileName: fileName,
				Metadata: map[string]any{
					"type":         "antigravity",
					"access_token": tt.savedToken,
				},
			}

			savedPath, errSave := store.Save(context.Background(), auth)
			if errSave != nil {
				t.Fatalf("Save() error = %v", errSave)
			}
			if savedPath != path {
				t.Fatalf("Save() path = %q, want %q", savedPath, path)
			}
			if got := auth.Attributes[cliproxyauth.AttributePath]; got != path {
				t.Errorf("path attribute = %q, want %q", got, path)
			}
			if got := auth.Attributes[cliproxyauth.AttributeSource]; got != path {
				t.Errorf("source attribute = %q, want %q", got, path)
			}
			if got := auth.Attributes[cliproxyauth.AttributeSourceBackend]; got != cliproxyauth.AuthSourceFile {
				t.Errorf("source backend attribute = %q, want %q", got, cliproxyauth.AuthSourceFile)
			}
			persisted, errRead := os.ReadFile(path)
			if errRead != nil {
				t.Fatalf("read saved auth file: %v", errRead)
			}
			expected := []byte(`{"type":"antigravity","access_token":"` + tt.savedToken + `","disabled":false}`)
			if !jsonEqual(persisted, expected) {
				t.Errorf("saved auth file = %s, want JSON equal to %s", persisted, expected)
			}
		})
	}
}

func TestFileTokenStoreNormalizesLegacyCredentialMetadata(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		baseDir := t.TempDir()
		store := NewFileTokenStore()
		store.SetBaseDir(baseDir)
		auth := &cliproxyauth.Auth{
			ID:       "legacy-save.json",
			FileName: "legacy-save.json",
			Metadata: map[string]any{
				"type":            "codex",
				"request-retry":   2,
				"request_retry":   0,
				"disable-cooling": true,
			},
		}

		path, errSave := store.Save(context.Background(), auth)
		if errSave != nil {
			t.Fatalf("Save() error = %v", errSave)
		}
		persisted, errRead := os.ReadFile(path)
		if errRead != nil {
			t.Fatalf("read saved auth file: %v", errRead)
		}
		want := []byte(`{"type":"codex","request_retry":0,"disable_cooling":true,"disabled":false}`)
		if !jsonEqual(persisted, want) {
			t.Fatalf("saved auth file = %s, want JSON equal to %s", persisted, want)
		}
	})

	t.Run("list", func(t *testing.T) {
		baseDir := t.TempDir()
		path := filepath.Join(baseDir, "legacy-list.json")
		if errWrite := os.WriteFile(path, []byte(`{"type":"codex","request-retry":2,"disable-cooling":true}`), 0o600); errWrite != nil {
			t.Fatalf("write legacy auth file: %v", errWrite)
		}
		store := NewFileTokenStore()
		store.SetBaseDir(baseDir)

		auths, errList := store.List(context.Background())
		if errList != nil {
			t.Fatalf("List() error = %v", errList)
		}
		if len(auths) != 1 {
			t.Fatalf("List() len = %d, want 1", len(auths))
		}
		if got := auths[0].Metadata["request_retry"]; got != float64(2) {
			t.Fatalf("listed request_retry = %#v, want 2", got)
		}
		if got := auths[0].Metadata["disable_cooling"]; got != true {
			t.Fatalf("listed disable_cooling = %#v, want true", got)
		}
		for _, legacy := range []string{"request-retry", "disable-cooling"} {
			if _, exists := auths[0].Metadata[legacy]; exists {
				t.Fatalf("listed metadata retained %q: %#v", legacy, auths[0].Metadata)
			}
		}
	})
}

func TestFileTokenStoreSaveRejectsInvalidWeight(t *testing.T) {
	baseDir := t.TempDir()
	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	auth := &cliproxyauth.Auth{
		ID:       "invalid.json",
		FileName: "invalid.json",
		Metadata: map[string]any{
			"type":                       "test",
			cliproxyauth.AttributeWeight: 1.5,
		},
	}

	if _, errSave := store.Save(context.Background(), auth); errSave == nil {
		t.Fatal("Save() accepted an invalid weight")
	}
	if _, errStat := os.Stat(filepath.Join(baseDir, auth.FileName)); !os.IsNotExist(errStat) {
		t.Fatalf("invalid auth file was persisted: %v", errStat)
	}
}
