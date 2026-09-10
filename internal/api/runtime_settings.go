package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

type SettingsLifecycleEvent struct {
	Operation string `json:"operation"`
}

type SettingsLifecycle interface {
	BeginSettingsList(context.Context, *SettingsLifecycleEvent) (func(error), error)
	BeginSettingsUpdate(context.Context, *SettingsLifecycleEvent) (func(error), error)
}

func beginSettingsLifecycle(hooks SettingsLifecycle, ctx context.Context, operation string) (func(error), error) {
	if hooks == nil {
		return func(error) {}, nil
	}
	event := &SettingsLifecycleEvent{Operation: operation}
	if operation == "list" {
		return hooks.BeginSettingsList(ctx, event)
	}
	return hooks.BeginSettingsUpdate(ctx, event)
}

// runtimeSettingsRoutes exposes one complete, revisioned operational document.
// PATCH merges supplied sections into one candidate, validates the complete
// result, and atomically persists it under the supplied revision precondition.
func runtimeSettingsRoutes(settings *runtimesettings.Controller, hooks SettingsLifecycle) []route {
	get := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finish, err := beginSettingsLifecycle(hooks, r.Context(), "list")
		if err != nil {
			finish(err)
			writeAPIError(w, r, APIError{Status: 500, Code: "settings_hook_failed", Message: "Settings request failed"})
			return
		}
		writeJSON(w, http.StatusOK, settings.Get())
		finish(nil)
	})
	patch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finish, hookErr := beginSettingsLifecycle(hooks, r.Context(), "update")
		if hookErr != nil {
			finish(hookErr)
			writeAPIError(w, r, APIError{Status: 500, Code: "settings_hook_failed", Message: "Settings request failed"})
			return
		}
		var patch map[string]json.RawMessage
		if decodeStrictJSON(r, &patch) != nil {
			finish(runtimesettings.ErrInvalid)
			writeRuntimeSettingsError(w, r, runtimesettings.ErrInvalid)
			return
		}
		input, err := mergeRuntimeSettings(settings.Get(), patch)
		if err != nil {
			finish(err)
			writeRuntimeSettingsError(w, r, runtimesettings.ErrInvalid)
			return
		}
		value, err := settings.Update(r.Context(), input)
		if err != nil {
			finish(err)
			writeRuntimeSettingsError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		finish(nil)
	})
	reload := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := settings.Reload(r.Context()); err != nil {
			writeRuntimeSettingsError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, settings.Get())
	})
	return []route{
		{method: "GET", pattern: "/api/admin/settings", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: get},
		{method: "PATCH", pattern: "/api/admin/settings", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: patch},
		{method: "POST", pattern: "/api/admin/settings/reload", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: reload},
	}
}

func mergeRuntimeSettings(current runtimesettings.Settings, patch map[string]json.RawMessage) (runtimesettings.Settings, error) {
	if _, ok := patch["revision"]; !ok {
		return runtimesettings.Settings{}, runtimesettings.ErrInvalid
	}
	allowed := map[string]bool{"revision": true, "batch": true, "logs": true, "proxy": true, "backups": true, "application": true, "rateLimits": true}
	for key := range patch {
		if !allowed[key] {
			return runtimesettings.Settings{}, runtimesettings.ErrInvalid
		}
	}
	base, err := json.Marshal(current)
	if err != nil {
		return runtimesettings.Settings{}, err
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(base, &document); err != nil {
		return runtimesettings.Settings{}, err
	}
	for key, value := range patch {
		if key == "revision" {
			document[key] = value
			continue
		}
		var existing, incoming map[string]json.RawMessage
		if err = json.Unmarshal(document[key], &existing); err != nil {
			return runtimesettings.Settings{}, err
		}
		if err = json.Unmarshal(value, &incoming); err != nil {
			return runtimesettings.Settings{}, err
		}
		for child := range incoming {
			if _, ok := existing[child]; !ok {
				return runtimesettings.Settings{}, runtimesettings.ErrInvalid
			}
		}
		for child, value := range incoming {
			existing[child] = value
		}
		joined, err := json.Marshal(existing)
		if err != nil {
			return runtimesettings.Settings{}, err
		}
		document[key] = joined
	}
	joined, err := json.Marshal(document)
	if err != nil {
		return runtimesettings.Settings{}, err
	}
	var result runtimesettings.Settings
	if err = json.Unmarshal(joined, &result); err != nil {
		return runtimesettings.Settings{}, err
	}
	return result, nil
}
func writeRuntimeSettingsError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: http.StatusServiceUnavailable, Code: "settings_unavailable", Message: "Runtime settings are unavailable"}
	if errors.Is(err, runtimesettings.ErrInvalid) {
		value = APIError{Status: 400, Code: "invalid_settings", Message: "Invalid runtime settings"}
	}
	if errors.Is(err, runtimesettings.ErrConflict) {
		value = APIError{Status: 409, Code: "settings_conflict", Message: "Runtime settings changed; reload and retry"}
	}
	writeAPIError(w, r, value)
}
