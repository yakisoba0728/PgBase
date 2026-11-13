package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
)

type nativeRulesService struct {
	RecordRules
	writes int
	rules  accessrule.Rules
}

func (s *nativeRulesService) Set(_ context.Context, collection string, rules accessrule.Rules) (recordaccess.RulePolicy, error) {
	s.writes++
	s.rules = rules
	return recordaccess.RulePolicy{Collection: collection, Rules: rules}, nil
}

func TestNativeRulesParseExpressionsAndTypedRulesBeforeOneWrite(t *testing.T) {
	service := &nativeRulesService{}
	var handler http.Handler
	for _, route := range ruleRoutes(Dependencies{RecordRules: service}) {
		if route.method == http.MethodPut {
			handler = route.handler
		}
	}
	for _, test := range []struct {
		expression string
		status     int
	}{{"title = 'open'", http.StatusOK}, {"title = 'open' &&", http.StatusBadRequest}, {" ", http.StatusBadRequest}} {
		body := `{"rules":{"list":{"op":"expression","expression":` + fmt.Sprintf("%q", test.expression) + `},"view":{"op":"owner"},"create":{"op":"deny"},"update":{"op":"owner"},"delete":{"op":"deny"}}}`
		request := httptest.NewRequest(http.MethodPut, "/api/admin/collections/posts/rules", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.SetPathValue("collection", "posts")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("expression %q: status=%d body=%s", test.expression, response.Code, response.Body.String())
		}
	}
	if service.writes != 1 || service.rules.List.Op != "eq" || service.rules.List.Expression == nil || service.rules.View.Op != "owner" || service.rules.Update.Op != "owner" {
		t.Fatalf("policy was not saved once and completely: %#v", service)
	}
}

func TestHistoricalRuleSourceReadsAsNativeExpression(t *testing.T) {
	var rule accessrule.Rule
	if err := json.Unmarshal([]byte(`{"op":"eq","field":"title","value":"open","pocketbase":"title = 'open'"}`), &rule); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rule)
	if err != nil || rule.Expression == nil || strings.Contains(string(encoded), "pocketbase") || !strings.Contains(string(encoded), `"expression":`) {
		t.Fatalf("historical source lost or written back: %s, %v", encoded, err)
	}
	for _, raw := range []string{
		`{"op":"allow","pocketbase":"a","expression":"b"}`,
		`{"op":"allow","unexpected":true}`,
		`{"op":"all","args":[{"op":"allow","unexpected":true}]}`,
	} {
		if err := json.Unmarshal([]byte(raw), &rule); err == nil {
			t.Fatalf("accepted ambiguous or unknown rule fields: %s", raw)
		}
	}
}

func TestRuleErrorsNameTheActionAndPosition(t *testing.T) {
	broken := "title = 'x' &&"
	_, err := accessrule.FromPocketBaseRulesParsed(accessrule.PocketBaseRules{Update: &broken}, query.ParseAccessRule)
	response := httptest.NewRecorder()
	writeRuleError(response, httptest.NewRequest(http.MethodPut, "/api/admin/collections/posts/rules?compat=pocketbase", nil), err)
	var body struct {
		Error struct {
			Code    string              `json:"code"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != http.StatusBadRequest || body.Error.Code != "invalid_rule" ||
		fmt.Sprint(body.Error.Details) != "map[rules.update:[invalid_syntax] rules.update.position:[15]]" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
