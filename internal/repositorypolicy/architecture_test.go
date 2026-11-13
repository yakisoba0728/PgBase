package repositorypolicy

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These boundaries apply to every source file, including newly added files.
// Implementation counts remain available from tools/repostats as diagnostics.
func TestCoreDependenciesPointTowardDomainServices(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"database", "query", "recordops", "relation", "schema", "recordidentity", "accessrule", "recordaccess", "membership"} {
		t.Run(name, func(t *testing.T) {
			err := filepath.WalkDir(filepath.Join(root, "internal", name), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if err != nil {
					return err
				}
				for _, imported := range file.Imports {
					dependency, err := strconv.Unquote(imported.Path.Value)
					if err != nil {
						return err
					}
					if forbiddenCoreDependency(name, dependency) {
						t.Errorf("%s imports outer-layer dependency %s", path, dependency)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func forbiddenCoreDependency(owner, dependency string) bool {
	const module = "github.com/yakisoba0728/PgBase/"
	if dependency == "net/http" || dependency == "os/exec" {
		return true
	}
	for _, outer := range []string{"internal/api", "internal/cli", "internal/jsvm", "pgbase", "pgbasecli", "ui", "sdk"} {
		if dependency == module+outer || strings.HasPrefix(dependency, module+outer+"/") {
			return true
		}
	}
	if owner == "database" {
		return strings.HasPrefix(dependency, module+"internal/")
	}
	if owner == "schema" || owner == "membership" {
		for _, upper := range []string{"query", "recordops", "recordaccess", "accessrule"} {
			if dependency == module+"internal/"+upper {
				return true
			}
		}
	}
	return owner == "query" && dependency == module+"internal/recordops"
}

func TestAdministratorPagesRemainRouteWrappers(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "ui", "admin", "src", "pages", "*.tsx"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("find administrator routes: %v", err)
	}
	directState := regexp.MustCompile(`\buse(?:State|Effect)\s*\(`)
	for _, page := range pages {
		source, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		if directState.Match(source) {
			t.Errorf("%s owns screen state instead of delegating to its feature view", page)
		}
	}
}
