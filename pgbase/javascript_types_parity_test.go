package pgbase

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestJavaScriptTypeDeclarationsCoverInstalledAppInventory(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/inventory","access":"public","handler":{"module":"route.js","export":"run"}}]}`, `exports.run=()=>Object.keys($app).sort()`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	raw, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatal(err)
	}
	declarations := JavaScriptTypeDeclarations()
	appDeclarations := strings.Join(regexp.MustCompile(`(?m)^    interface App \{.*$`).FindAllString(declarations, -1), "\n")
	for _, name := range names {
		member := regexp.MustCompile(`(?:^|[; ]+)` + regexp.QuoteMeta(name) + `(?:<[^>]+>)?\s*\(`)
		if !member.MatchString(appDeclarations) {
			t.Errorf("installed $app.%s is missing from declarations", name)
		}
	}
}

func TestJavaScriptTypeDeclarationArtifactIsCurrent(t *testing.T) {
	artifact, err := os.ReadFile("../sdk/javascript/pgbase.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact) != JavaScriptTypeDeclarations() {
		t.Fatal("sdk/javascript/pgbase.d.ts is not synchronized with JavaScriptTypeDeclarations")
	}
}
