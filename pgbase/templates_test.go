package pgbase

import "testing"

func TestTemplateRegistryCompilesEscapesAndRequiresFields(t *testing.T) {
	registry := &TemplateRegistry{}
	if err := registry.Register("welcome", `<h1>Hello {{.name}}</h1>`); err != nil {
		t.Fatal(err)
	}
	output, err := registry.Render("welcome", map[string]any{"name": `<Ada>`})
	if err != nil || output != `<h1>Hello &lt;Ada&gt;</h1>` {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if _, err := registry.Render("welcome", map[string]any{}); err == nil {
		t.Fatal("missing field accepted")
	}
}
