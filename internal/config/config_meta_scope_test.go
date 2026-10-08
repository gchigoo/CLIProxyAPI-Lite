package config

import (
	"reflect"
	"strings"
	"testing"
)

// Lite supports Meta OAuth accounts only; a meta-api-key section must not return.
func TestConfigHasNoMetaAPIKeySection(t *testing.T) {
	configType := reflect.TypeOf(Config{})
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)
		name := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if name == "meta-api-key" || field.Name == "MetaKey" {
			t.Fatalf("Config.%s restores the excluded meta-api-key section", field.Name)
		}
	}
}
