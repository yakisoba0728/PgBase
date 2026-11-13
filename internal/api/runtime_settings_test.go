package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/validation"
)

func TestMergeRuntimeSettingsRejectsNullInsteadOfZeroing(t *testing.T) {
	current := runtimesettings.Default()
	current.Backups.Keep = 7
	current.Logs.IncludeIP = true
	current.Application.Accent = "#123456"
	current.Proxy.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	for _, test := range []struct{ patch, field string }{
		{`{"revision":1,"backups":{"keep":null}}`, "backups.keep"},
		{`{"revision":1,"logs":{"includeIP":null}}`, "logs.includeIP"},
		{`{"revision":1,"application":{"accent":null}}`, "application.accent"},
		{`{"revision":1,"proxy":{"trustedProxyCIDRs":null}}`, "proxy.trustedProxyCIDRs"},
		{`{"revision":1,"rateLimits":{"rules":[{"label":"GET /api/","audience":null,"duration":1,"maxRequests":1}]}}`, "rateLimits.rules.0.audience"},
		{`{"revision":1,"batch":null}`, "batch"},
		{`{"revision":1,"logs":{"unknown":1}}`, "logs.unknown"},
	} {
		var patch map[string]json.RawMessage
		if err := json.Unmarshal([]byte(test.patch), &patch); err != nil {
			t.Fatal(err)
		}
		_, err := mergeRuntimeSettings(current, patch)
		var invalid *validation.Error
		if !errors.Is(err, runtimesettings.ErrInvalid) || !errors.As(err, &invalid) || fmt.Sprint(invalid.Details) != fmt.Sprintf("[{%s invalid}]", test.field) {
			t.Fatalf("%s: err=%v", test.patch, err)
		}
	}
	merged, err := mergeRuntimeSettings(current, map[string]json.RawMessage{"revision": json.RawMessage(`1`), "backups": json.RawMessage(`{"enabled":true}`)})
	if err != nil || merged.Backups.Keep != 7 || !merged.Backups.Enabled || !merged.Logs.IncludeIP || merged.Application.Accent != "#123456" {
		t.Fatalf("omitted fields changed: %#v err=%v", merged, err)
	}
}
