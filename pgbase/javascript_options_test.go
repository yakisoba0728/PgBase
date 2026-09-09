package pgbase

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateJavaScriptReturnsSourceBoundManifestAndClonesCapabilities(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/hello","access":"public","handler":{"module":"route.js","export":"hello"},"capabilities":["records"]}]}`
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(`exports.hello=()=>({status:200,json:{hello:"world"}});`), 0o600); err != nil {
		t.Fatal(err)
	}
	options := JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true}}
	validated, err := ValidateJavaScript(context.Background(), options)
	if err != nil {
		t.Fatalf("validate JavaScript: %v", err)
	}
	if validated.Digest == "" || len(validated.Routes) != 1 || validated.Routes[0].Pattern != "/hello" {
		t.Fatalf("validated manifest = %#v", validated)
	}

	base := Options{DatabaseURL: "postgres://postgres:postgres@localhost/pgbase", TokenSecret: make([]byte, 32)}
	base.JavaScript = &options
	normalized, err := base.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	options.Capabilities["records"] = false
	if normalized.JavaScript == nil || !normalized.JavaScript.Capabilities["records"] {
		t.Fatal("Normalized retained caller-owned JavaScript capability map")
	}
}
