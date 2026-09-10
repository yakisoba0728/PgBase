//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestSettingsLifecycleObservesOnlySuccessfulPublication(t *testing.T) {
	events := make(chan []string, 4)
	registry := &HookRegistry{}
	_, err := registry.On(HookSettingsReload, Hook{After: func(_ context.Context, event *HookEvent) {
		events <- append([]string(nil), event.Data.(*SettingsReloadEvent).Changed...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) { o.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	response := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/settings", nil, "", cookie, nil)
	assertPublicUsersStatus(t, response, http.StatusOK)
	var initial map[string]any
	decodePublicUsersJSON(t, response, &initial)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	patch := map[string]any{"revision": initial["revision"], "application": map[string]any{"theme": "dark"}}
	response = publicUsersRequest(t, fixture, http.MethodPatch, "/api/admin/settings", patch, "", cookie, headers)
	assertPublicUsersStatus(t, response, http.StatusOK)
	select {
	case changed := <-events:
		if !reflect.DeepEqual(changed, []string{"application"}) {
			t.Fatalf("changed settings = %v", changed)
		}
	default:
		t.Fatal("committed settings did not emit a lifecycle event")
	}
	response = publicUsersRequest(t, fixture, http.MethodPatch, "/api/admin/settings", patch, "", cookie, headers)
	assertPublicUsersStatus(t, response, http.StatusConflict)
	select {
	case changed := <-events:
		t.Fatalf("rejected settings emitted an event: %v", changed)
	default:
	}
	response = publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/settings/reload", nil, "", cookie, headers)
	assertPublicUsersStatus(t, response, http.StatusOK)
	select {
	case changed := <-events:
		if len(changed) != 0 {
			t.Fatalf("unchanged reload reported changes: %v", changed)
		}
	default:
		t.Fatal("explicit reload did not emit a lifecycle event")
	}
}
