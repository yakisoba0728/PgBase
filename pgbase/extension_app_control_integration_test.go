//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

func TestExtensionAppControlUsesCurrentAppStateAndAdministratorAuthority(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	control := fixture.app.extensions.appControl
	var restarted atomic.Int32
	control.restart = func(context.Context) error { restarted.Add(1); return nil }
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, valid: true}
	call := func(method, input string) json.RawMessage {
		t.Helper()
		result, err := host.callAppControl(t.Context(), method, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return result
	}
	if got := string(call("app-control.isBootstrapped", `{}`)); got != "true" {
		t.Fatalf("isBootstrapped=%s", got)
	}
	settings := call("app-control.settings", `{}`)
	if !json.Valid(settings) || !containsJSONKey(settings, "revision") {
		t.Fatalf("settings=%s", settings)
	}
	if got := string(call("app-control.storeSet", `{"key":"shared","value":{"n":1}}`)); got != `{"n":1}` {
		t.Fatalf("storeSet=%s", got)
	}
	if got := string(call("app-control.storeGet", `{"key":"shared"}`)); got != `{"n":1}` {
		t.Fatalf("storeGet=%s", got)
	}
	call("app-control.reloadSettings", `{}`)
	call("app-control.runSystemMigrations", `{}`)
	call("app-control.restart", `{}`)
	if restarted.Load() != 1 {
		t.Fatalf("restart callbacks=%d", restarted.Load())
	}
	denied := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, valid: true}
	if _, err := denied.callAppControl(t.Context(), "app-control.settings", json.RawMessage(`{}`)); err == nil {
		t.Fatal("user authority reached app control")
	}
}

func containsJSONKey(raw json.RawMessage, key string) bool {
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value[key] != nil
}
