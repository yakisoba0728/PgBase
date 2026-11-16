package api

import (
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
)

// withRuleRequestContext runs inside the route's admission/authentication
// wrappers, so the snapshot includes the authenticated principal. Services keep
// their own authorization checks and never read transport metadata from globals.
func withRuleRequestContext(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := accessrule.WithRequestContext(r.Context(), ruleRequestContext(r))
		handler(w, r.WithContext(ctx))
	}
}
