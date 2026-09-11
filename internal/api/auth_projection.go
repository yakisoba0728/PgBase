package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	neturl "net/url"

	"github.com/yakisoba0728/PgBase/internal/responseprojection"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

// compileAuthProjection validates fields before an authentication mutation can
// run. Projection applies only to the already-safe response transport structs.
func compileAuthProjection(w http.ResponseWriter, r *http.Request) (responseprojection.Projection, bool) {
	projection, err := authProjection(r)
	if err != nil {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return responseprojection.Projection{}, false
	}
	return projection, true
}

func authProjection(r *http.Request) (responseprojection.Projection, error) {
	values, err := neturl.ParseQuery(r.URL.RawQuery)
	if err != nil || duplicateSingleton(values, "fields") {
		return responseprojection.Projection{}, userauth.ErrValidation
	}
	projection, err := responseprojection.CompileProjection(values.Get("fields"))
	if err != nil {
		return responseprojection.Projection{}, userauth.ErrValidation
	}
	return projection, nil
}

func writeAuthProjectedJSON(w http.ResponseWriter, status int, projection responseprojection.Projection, value any) {
	if projection.Empty() {
		writeJSON(w, status, value)
		return
	}
	// Decode with UseNumber so projection preserves the lexical representation
	// of numeric fields from map-backed profile and administrator responses.
	raw, err := json.Marshal(value)
	if err != nil {
		writeJSON(w, status, value)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		writeJSON(w, status, value)
		return
	}
	writeJSON(w, status, projection.Apply(document))
}
