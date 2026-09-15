package test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLiteHasNoDynamicPluginOrDevinDependencies(t *testing.T) {
	const module = "github.com/router-for-me/CLIProxyAPI/v7/"
	for _, directory := range []string{"cmd", "internal", "sdk"} {
		err := filepath.WalkDir(filepath.Join("..", directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := strings.ToLower(entry.Name())
			if entry.IsDir() {
				if name == "devin" || name == "pluginhost" || name == "pluginabi" || name == "homeplugins" || strings.HasPrefix(name, "pluginapi") || name == "pluginstore" {
					t.Errorf("removed extension directory restored: %s", path)
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") {
				return nil
			}
			if strings.HasPrefix(name, "devin_") {
				t.Errorf("removed Devin source restored: %s", path)
			}
			file, errParse := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if errParse != nil {
				return errParse
			}
			for _, spec := range file.Imports {
				dependency, errUnquote := strconv.Unquote(spec.Path.Value)
				if errUnquote != nil {
					return errUnquote
				}
				if dependency == "plugin" || strings.HasPrefix(dependency, "github.com/ebitengine/purego") ||
					strings.HasPrefix(dependency, module+"internal/plugin") || strings.HasPrefix(dependency, module+"internal/homeplugins") ||
					strings.HasPrefix(dependency, module+"sdk/plugin") || strings.HasPrefix(dependency, module+"internal/auth/devin") {
					t.Errorf("%s imports removed dynamic extension dependency %s", path, dependency)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
