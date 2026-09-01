package repositorypolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlockingLintToolchainIsPinnedAndNarrow(t *testing.T) {
	root := filepath.Join("..", "..")
	script, err := os.ReadFile(filepath.Join(root, "scripts", "lint-go.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "golangci-lint@v2.12.2") {
		t.Fatal("lint runner does not pin the approved Go 1.26-compatible golangci-lint release")
	}

	configuration, err := os.ReadFile(filepath.Join(root, ".golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(configuration)
	for _, marker := range []string{"default: none", "- errcheck", "- ineffassign", "- unparam", "- unused", "- wastedassign", "tests: false"} {
		if !strings.Contains(text, marker) {
			t.Errorf("blocking lint configuration is missing %q", marker)
		}
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "scripts/lint-go.sh") {
		t.Fatal("backend workflow does not run the pinned blocking lint set")
	}
}
