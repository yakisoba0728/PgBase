package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
)

type RecordRules interface {
	Get(context.Context, string) (recordaccess.RulePolicy, error)
	Set(context.Context, string, accessrule.Rules) (recordaccess.RulePolicy, error)
	Preview(context.Context, string, recordaccess.RulePreview) ([]recordaccess.RuleDecision, error)
}

func ruleRoutes(dependencies Dependencies) []route {
	if dependencies.RecordRules == nil {
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result recordaccess.RulePolicy
		var err error
		if r.Method == "GET" {
			result, err = dependencies.RecordRules.Get(r.Context(), r.PathValue("collection"))
		} else {
			var input struct {
				Rules json.RawMessage `json:"rules"`
			}
			if decodeStrictJSON(r, &input) != nil {
				writeRuleError(w, r, accessrule.ErrInvalid)
				return
			}
			rules, parseErr := accessrule.Parse(input.Rules)
			if parseErr != nil {
				writeRuleError(w, r, parseErr)
				return
			}
			result, err = dependencies.RecordRules.Set(r.Context(), r.PathValue("collection"), rules)
		}
		if err != nil {
			writeRuleError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	})
	preview := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input recordaccess.RulePreview
		if decodeStrictJSON(r, &input) != nil {
			writeRuleError(w, r, accessrule.ErrInvalid)
			return
		}
		result, err := dependencies.RecordRules.Preview(r.Context(), r.PathValue("collection"), input)
		if err != nil {
			writeRuleError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"items": result})
	})
	return []route{
		{method: "POST", pattern: "/api/admin/collections/{collection}/rules/preview", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: 64 << 10, handler: preview},
		{method: "GET", pattern: "/api/admin/collections/{collection}/rules", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: handler},
		{method: "PUT", pattern: "/api/admin/collections/{collection}/rules", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: 64 << 10, handler: handler},
	}
}

func writeRuleError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: 503, Code: "rules_unavailable", Message: "Record rules are unavailable"}
	if errors.Is(err, accessrule.ErrInvalid) {
		response = APIError{Status: 400, Code: "invalid_rule", Message: "Invalid typed access rule"}
	}
	if errors.Is(err, recordaccess.ErrNotFound) {
		response = APIError{Status: 404, Code: "not_found", Message: "Collection not found"}
	}
	writeAPIError(w, r, response)
}
