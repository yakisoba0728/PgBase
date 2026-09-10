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

	"github.com/yakisoba0728/PgBase/internal/query"
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
	//
	// Ratcheted from 4,910 by R4 Release C, which retires cursor v4: +9/-25 for
	// the decode case and its sort-key comparison in root_page.go, the encode
	// branch in page_assembler.go, and the issue-version guard in
	// cursor_v5_binding.go that the retired boundary makes constant. Removing
	// the last v4 branch collapsed a three-way version switch into one check,
	// so the arithmetic is a net -16 rather than the -25 deleted.
	//
	// Ratcheted from 4,894 by R11: +0/-10 for the NewService forwarder, which
	// defaulted the logger and the recorder and had no caller in the tree, not
	// even a test. NewServiceWithRecorder took its name.
	// M1 adds owner-scoped SQL and cursor binding: measured 4,872 -> 4,911
	// (+42/-3), preserving all existing rationale. This feature scope expansion
	// is included in docs/public-identity-owner-access-design.md and the group
	// ceiling. No extra headroom is allocated.
	// M2–M6 adds relation target rules, visibility hydration and source-backed views.
	// See docs/local-milestone-design.md for the measured scope change.
	// P7 adds hidden-field path validation and response projection; measured
	// accounting is in docs/phase3-local-contracts.md with no spare allowance.
	// P8 adds coordinate paths and advanced-field codecs; measured scope is
	// in docs/phase4-local-contracts.md with no spare allowance.
	// R3 adds schema-validated response projection after authorization,
	// expansion, cursor assembly, and ETag calculation: +74/-0.
	// PocketBase-compatible response picking remains in responseprojection;
	// this increase pays only for reusable field comparison and event predicate
	// compilation at the query boundary.
	// The 68-feature PocketBase completion adds the shared scalar predicate,
	// relation expansion, response projection, and compatibility pagination
	// paths measured here. Keep the exact post-completion measurement.
	// RULE-03 adds the bounded PocketBase operand grammar, JSON extraction and
	// function renderers at this existing query seam. The measured increase has
	// no spare allowance.
	// EXT14 also adds an exact, repeatable-read count facade without widening
	// the exported Service method surface.
	if runtimeLines > 7140 {
		t.Fatalf("internal/query runtime lines = %d, hard limit 7140", runtimeLines)
	}
}
