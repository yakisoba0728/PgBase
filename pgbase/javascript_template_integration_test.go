//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptUsesSharedTemplateRegistry(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/extensions/template","access":"public","handler":{"module":"route.ts","export":"render"},"capabilities":["template"]}]}`
	source := `import pg=require("pgbase"); type Input={json:{name:string}}; export function render(input:Input){const value=pg.template.render({name:"welcome",data:input.json});return {status:200,body:value.output}}`
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	templates := &TemplateRegistry{}
	if err := templates.Register("welcome", `Hello {{.name}}`); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Templates = templates
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"template": true}}
	})
	response := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/template", map[string]any{"name": `<Ada>`}, "", nil, nil)
	if response.Code != http.StatusOK || response.Body.String() != `Hello &lt;Ada&gt;` {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
