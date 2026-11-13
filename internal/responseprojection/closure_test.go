package responseprojection

import (
	"encoding/json"
	"testing"
)

func TestProjectionSelectsRawJSONChildrenWithoutRounding(t *testing.T) {
	p, err := CompileProjection("payload.public,items.public")
	if err != nil {
		t.Fatal(err)
	}
	got := p.Apply(map[string]any{"payload": json.RawMessage(`{"public":9007199254740993,"private":"hidden"}`), "items": json.RawMessage(`[{"public":1,"private":2}]`)})
	raw, err := json.Marshal(got)
	if err != nil || string(raw) != `{"items":[{"public":1}],"payload":{"public":9007199254740993}}` {
		t.Fatalf("%s %v", raw, err)
	}
}
