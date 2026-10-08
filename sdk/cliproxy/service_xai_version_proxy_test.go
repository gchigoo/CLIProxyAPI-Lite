package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// The Grok CLI version lookup must follow a reloaded global proxy, otherwise a
// proxy enabled after startup would leave npm lookups on the old route.
func TestConfigReloadUpdatesXAIVersionProxy(t *testing.T) {
	previous := helps.XAIVersionProxyURL()
	t.Cleanup(func() { helps.SetXAIVersionProxyURL(previous) })
	helps.SetXAIVersionProxyURL("")

	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	reloaded := &config.Config{}
	reloaded.ProxyURL = "socks5://127.0.0.1:10808"
	service.applyWatcherConfigUpdate(reloaded)

	if got := helps.XAIVersionProxyURL(); got != "socks5://127.0.0.1:10808" {
		t.Fatalf("XAIVersionProxyURL() = %q after reload, want socks5://127.0.0.1:10808", got)
	}
}
