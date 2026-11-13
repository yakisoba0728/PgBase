package query_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/query"
)

type readKernel interface {
	List(context.Context, query.ListRequest) (query.RecordPage, error)
	Get(context.Context, query.GetRequest) (query.Record, error)
	Count(context.Context, query.ListRequest) (int64, error)
}

var _ readKernel = (*query.Service)(nil)

func TestReadKernelDoesNotDependOnTransportsOrExtensionRuntime(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate query sources")
	}
	entries, err := os.ReadDir(filepath.Dir(currentFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "expand.go" || entry.Name() == "root_plan.go" {
			source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, adapter := range []string{"PocketBaseCompatibility", "pocketBaseSortKeys", "normalizePocketBaseExpand", "responseprojection.PocketBaseRecord"} {
				if strings.Contains(string(source), adapter) {
					t.Errorf("native kernel %s contains legacy adapter %s", entry.Name(), adapter)
				}
			}
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(currentFile), entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, boundary := range []string{"net/http", "github.com/yakisoba0728/PgBase/internal/api", "github.com/yakisoba0728/PgBase/internal/cli", "github.com/yakisoba0728/PgBase/internal/jsvm", "github.com/yakisoba0728/PgBase/pgbase"} {
				if name == boundary || strings.HasPrefix(name, boundary+"/") {
					t.Errorf("%s imports outer boundary %s", entry.Name(), name)
				}
			}
		}
	}
}
