package pgbase

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtensionRecordQueryCompilesTypedDBXAndRejectsRawSQL(t *testing.T) {
	raw := json.RawMessage(`{"op":"and","args":[{"op":"in","args":["id",["a","b"]]},{"op":"like","args":["title","needle"]}]}`)
	filter, err := compileExtensionDBX(raw)
	if err != nil || !strings.Contains(filter, "id = 'a'") || !strings.Contains(filter, "title ~ 'needle'") {
		t.Fatalf("filter=%s err=%v", filter, err)
	}
	attack := json.RawMessage(`{"op":"exp","args":["true); delete from pgbase_meta.users; --",{}]}`)
	if _, err := compileExtensionDBX(attack); err == nil {
		t.Fatal("raw SQL expression reached ordinary record query")
	}
}
