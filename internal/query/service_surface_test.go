package query_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/query"
)

type readKernel interface {
	List(context.Context, query.ListRequest) (query.RecordPage, error)
	Get(context.Context, query.GetRequest) (query.Record, error)
}

var _ readKernel = (*query.Service)(nil)

func TestReadKernelServiceSurfaceIsListAndGet(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate service surface test")
	}
	entries, err := os.ReadDir(filepath.Dir(currentFile))
	if err != nil {
		t.Fatal(err)
	}
	methods := make([]string, 0, 2)
	files := token.NewFileSet()
	runtimeLines := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(filepath.Dir(currentFile), entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		runtimeLines += bytes.Count(source, []byte{'\n'})
		if len(source) != 0 && source[len(source)-1] != '\n' {
			runtimeLines++
		}
		file, err := parser.ParseFile(files, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || !function.Name.IsExported() || len(function.Recv.List) != 1 {
				continue
			}
			receiver := function.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			name, ok := receiver.(*ast.Ident)
			if ok && name.Name == "Service" {
				methods = append(methods, function.Name.Name)
			}
		}
	}
	sort.Strings(methods)
	if strings.Join(methods, ",") != "Get,List" {
		t.Fatalf("query.Service exported methods = %v, want [Get List]", methods)
	}
	// A per-package ratchet, not a plan target. The plan budgets this group
	// (database+query+recordops+relation+schema) as a whole at 12,750, and R11
	// owns bringing it there; nothing here may drift upward on its own.
	//
	// Re-based from 4,850 by the Release B correctness stack, which plan
	// section 11 allows to be LOC-positive as long as the category is stated:
	// +93/-26 for cursor v5 membership binding plus two audit fixes — keeping a
	// filter validation error from being reported as an invalid cursor, and
	// rejecting a jsonb root filter at plan time so the statement compiler and
	// the cursor fingerprint cannot disagree about which detail the caller gets.
	// Leave no slack: raise this only alongside the deletion that pays for it.
	//
	// Ratcheted from 4,917 by R5 section 9.2, which paid for its own additions:
	// the dead CompileDelete, and resolveOrderSpec once planRelationSort's root
	// terms became identical to it.
	if runtimeLines > 4_910 {
		t.Fatalf("internal/query runtime lines = %d, hard limit 4910", runtimeLines)
	}
}
