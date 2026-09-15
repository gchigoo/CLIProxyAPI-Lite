package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher"
)

func TestHandleAuthUpdates_UnconfiguredDuplicateBatchCompletes(t *testing.T) {
	svc := &Service{}
	update := watcher.AuthUpdate{Action: watcher.AuthUpdateActionModify, ID: "unconfigured-auth"}
	update.SetRevision(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.handleAuthUpdates(context.Background(), []watcher.AuthUpdate{update, update})
	}()
	t.Cleanup(func() {
		if ch := svc.authRegistrationWaitCh(update.ID); ch != nil {
			finishAuthRegistrations(svc, []authRegistrationWait{{id: update.ID, ch: ch}})
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("auth update did not exit after cleanup")
		}
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unconfigured batch waited for its own unfinished registration")
	}
	if ch := svc.authRegistrationWaitCh(update.ID); ch != nil {
		t.Fatal("unconfigured batch retained a registration waiter")
	}
}
