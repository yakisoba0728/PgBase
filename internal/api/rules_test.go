package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
)

func TestRuleErrorsNameTheActionAndPosition(t *testing.T) {
	broken := "title = 'x' &&"
	_, err := accessrule.FromPocketBaseRulesParsed(accessrule.PocketBaseRules{Update: &broken}, query.ParsePocketBaseAccessRule)
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
