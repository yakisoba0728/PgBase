package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type RecordRules interface {
	Get(context.Context, string) (recordaccess.RulePolicy, error)
	SetAtRevision(context.Context, string, accessrule.Rules, *int64) (recordaccess.RulePolicy, error)
	Preview(context.Context, string, recordaccess.RulePreview) ([]recordaccess.RuleDecision, error)
}

func ruleRoutes(dependencies Dependencies) []route {
	if dependencies.RecordRules == nil {
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result recordaccess.RulePolicy
		var err error
		collection := r.PathValue("collection")
		compat := r.URL.Query().Get("compat") == "pocketbase"
		if r.Method == http.MethodGet {
			result, err = dependencies.RecordRules.Get(r.Context(), collection)
		} else {
			rules, expected, parseErr := decodeRuleUpdate(r, compat)
			if parseErr != nil {
				writeRuleError(w, r, parseErr)
				return
			}
			result, err = dependencies.RecordRules.SetAtRevision(r.Context(), collection, rules, expected)
		}
		if err != nil {
			writeRuleError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if compat {
			writeJSON(w, http.StatusOK, map[string]any{"collection": result.Collection, "pocketbaseRules": result.Rules.PocketBase(), "revision": result.Revision})
			return
		}
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

func decodeRuleUpdate(r *http.Request, compat bool) (accessrule.Rules, *int64, error) {
	if compat {
		var input struct {
			Rules    accessrule.PocketBaseRules `json:"pocketbaseRules"`
			Revision *int64                     `json:"revision"`
		}
		if decodeStrictJSON(r, &input) != nil || (input.Revision != nil && *input.Revision < 0) {
			return accessrule.Rules{}, nil, accessrule.ErrInvalid
		}
		rules, err := accessrule.FromPocketBaseRulesParsed(input.Rules, query.ParseAccessRule)
		return rules, input.Revision, err
	}
	var input struct {
		Rules    json.RawMessage `json:"rules"`
		Revision *int64          `json:"revision"`
	}
	if decodeStrictJSON(r, &input) != nil || (input.Revision != nil && *input.Revision < 0) {
		return accessrule.Rules{}, nil, accessrule.ErrInvalid
	}
	rules, err := query.ParseAccessRules(input.Rules)
	return rules, input.Revision, err
}

func writeRuleError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: 503, Code: "rules_unavailable", Message: "Record rules are unavailable"}
	if errors.Is(err, accessrule.ErrInvalid) {
		response = APIError{Status: 400, Code: "invalid_rule", Message: "Invalid access rule"}
		var validation *schema.ValidationError
		if errors.As(err, &validation) {
			response.Details = map[string][]string{}
			for _, detail := range validation.Details {
				response.Details[detail.Field] = append(response.Details[detail.Field], detail.Code)
			}
		}
	}
	if errors.Is(err, recordaccess.ErrUnsupported) {
		writeRecordError(w, r, err)
		return
	}
	if errors.Is(err, recordaccess.ErrRuleRevisionConflict) {
		response = APIError{Status: http.StatusPreconditionFailed, Code: "precondition_failed", Message: "Collection rules have changed"}
	}
	if errors.Is(err, recordaccess.ErrNotFound) {
		response = APIError{Status: 404, Code: "not_found", Message: "Collection not found"}
	}
	writeAPIError(w, r, response)
}
