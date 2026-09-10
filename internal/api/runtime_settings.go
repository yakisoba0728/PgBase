package api

import (
	"errors"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

// runtimeSettingsRoutes exposes one complete, revisioned operational document.
// PATCH deliberately accepts the whole candidate: validation is atomic and a
// caller can never persist one half of a mutually dependent configuration.
func runtimeSettingsRoutes(settings *runtimesettings.Controller) []route {
	get := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, settings.Get()) })
	patch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input runtimesettings.Settings
		if decodeStrictJSON(r, &input) != nil {
			writeRuntimeSettingsError(w, r, runtimesettings.ErrInvalid)
			return
		}
		value, err := settings.Update(r.Context(), input)
		if err != nil {
			writeRuntimeSettingsError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
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
