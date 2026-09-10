package schema

import "testing"

func TestRenameRuleCollectionReferencesSkipsLiteralValues(t *testing.T) {
	document := map[string]any{
		"field":      "@collection.old_name.title",
		"otherField": "@collection.old_name.slug",
		"value":      "@collection.old_name.literal",
		"args": []any{map[string]any{
			"pocketbase": "@collection.old_name.enabled = true && title = '@collection.old_name.literal'",
		}},
	}
	if !renameRuleCollectionReferences(document, "old_name", "new_name") {
		t.Fatal("reference rename reported no change")
	}
	if document["field"] != "@collection.new_name.title" || document["otherField"] != "@collection.new_name.slug" || document["value"] != "@collection.old_name.literal" {
		t.Fatalf("renamed document = %#v", document)
	}
	source := `@collection.old_name.enabled = true && title = '@collection.old_name.literal'`
	if got := replaceRuleSource(source, "@collection.old_name.", "@collection.new_name."); got != `@collection.new_name.enabled = true && title = '@collection.old_name.literal'` {
		t.Fatalf("renamed source = %q", got)
	}
}
