package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EDoesNotReintroduceWallClockPolling(t *testing.T) {
	entries, err := os.ReadDir("../e2e")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join("..", "e2e", entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"time.Sleep(", "time.Now().Add(", "time.Now().Before(", "time.Now().After("} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s contains raw wall-clock poll primitive %q; use Poll with the parent context", path, forbidden)
			}
		}
	}
}

func TestE2EComposeStartupHasABoundedContext(t *testing.T) {
	raw, err := os.ReadFile("../e2e/e2e_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `suite.compose(context.Background(), "up"`) {
		t.Fatal("E2E TestMain starts Compose with an unbounded background context")
	}
}
