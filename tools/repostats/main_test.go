package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeUsesStableMetricDefinitions(t *testing.T) {
	longLines := strings.Repeat("const x = 1\n", 801)
	files := []fileSnapshot{
		{
			Path: "internal/example/service.go",
			Data: []byte("package example\n\n// Service contract.\ntype Service interface { Run() }\ntype private interface { stop() }\ntype Exported struct{}\nfunc NewService() *Exported { return &Exported{} }\nfunc (Exported) Run() {}\n"),
		},
		{Path: "internal/example/service_test.go", Data: []byte("package example\nfunc TestService() {}\n")},
		{Path: "tools/helper.go", Data: []byte("package main\ntype ToolInterface interface{}\n")},
		{
			Path: "ui/admin/src/pages/example-page.tsx",
			Data: []byte("import { useEffect, useState } from \"react\";\n// runtime comment mentioning useEffect()\nexport function Page() {\n  const label = \"useState(ignored)\";\n  const [value] = useState<number>(0); useEffect(() => {}, []);\n  return label + value;\n}\n"),
		},
		{Path: "ui/admin/src/styles.css", Data: []byte("/* style comment */\n* { box-sizing: border-box; }\n")},
		{Path: "ui/admin/src/example.test.ts", Data: []byte("// test comment\n/** test doc */\nconst url = \"postgres://secret\";\nexport const fixture = 1;\n")},
		{Path: "ui/admin/src/test/setup.ts", Data: []byte("// helper comment\n")},
		{Path: "docs/guide.md", Data: []byte("# Guide\n")},
		{Path: "internal/example/long.go", Data: []byte("package example\n" + longLines)},
	}

	got, err := analyze(files)
	if err != nil {
		t.Fatal(err)
	}

	assertCount(t, "Go runtime", got.Runtime.Go, count{Files: 2, LOC: 810})
	assertCount(t, "UI runtime", got.Runtime.UI, count{Files: 2, LOC: 9})
	assertCount(t, "handwritten runtime", got.Runtime.Handwritten, count{Files: 4, LOC: 819})
	assertCount(t, "Go tests", got.Tests.Go, count{Files: 1, LOC: 2})
	assertCount(t, "Vitest", got.Tests.Vitest, count{Files: 1, LOC: 4})
	assertCount(t, "docs", got.Docs, count{Files: 1, LOC: 1})

	if got.ProductionGo.InterfaceDeclarations != 2 {
		t.Fatalf("interfaces = %d, want 2", got.ProductionGo.InterfaceDeclarations)
	}
	if got.ProductionGo.ExportedDeclarations != 4 {
		t.Fatalf("exported declarations = %d, want 4", got.ProductionGo.ExportedDeclarations)
	}
	if got.ProductionGo.NewConstructors != 1 {
		t.Fatalf("constructors = %d, want 1", got.ProductionGo.NewConstructors)
	}
	if got.Comments.GoRuntime != 1 {
		t.Fatalf("Go comments = %d, want 1", got.Comments.GoRuntime)
	}
	if got.Comments.UIRuntime != 2 {
		t.Fatalf("UI runtime comments = %d, want 2", got.Comments.UIRuntime)
	}
	if got.Comments.UIAllSource != 5 {
		t.Fatalf("all UI comments = %d, want 5", got.Comments.UIAllSource)
	}
	if got.Comments.UIExcludedNonRuntimeCommentLike != 3 {
		t.Fatalf("excluded UI comments = %d, want 3", got.Comments.UIExcludedNonRuntimeCommentLike)
	}
	if got.Comments.UIVitestCommentLike != 2 {
		t.Fatalf("Vitest comments = %d, want 2", got.Comments.UIVitestCommentLike)
	}
	if got.Comments.UILegacyRegexAllSource != 6 {
		t.Fatalf("legacy-regex UI comment lines = %d, want 6", got.Comments.UILegacyRegexAllSource)
	}
	if got.UI.PageDirectStateEffectOccurrences != 2 {
		t.Fatalf("page hooks = %d, want 2", got.UI.PageDirectStateEffectOccurrences)
	}
	if got.UI.PageDirectUseStateOccurrences != 1 || got.UI.PageDirectUseEffectOccurrences != 1 {
		t.Fatalf("page hook split = %#v, want one state and one effect", got.UI)
	}
	if got.UI.RuntimeExports != 1 {
		t.Fatalf("UI exports = %d, want 1", got.UI.RuntimeExports)
	}
	if len(got.LongProductionFiles) != 1 || got.LongProductionFiles[0].Path != "internal/example/long.go" || got.LongProductionFiles[0].LOC != 802 {
		t.Fatalf("long files = %#v, want internal/example/long.go at 802 LOC", got.LongProductionFiles)
	}
}

func TestCompareToWorktreeCategorizesGrossChanges(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	writeFile(t, root, "internal/example.go", "package example\nconst Old = 1\n")
	writeFile(t, root, "docs/guide.md", "old docs\n")
	writeFile(t, root, "ui/admin/dist/index.js", "old generated\n")
	writeFile(t, root, "deployments/app.yml", "old: config\n")
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=R0 Test", "-c", "user.email=r0@example.invalid", "commit", "-qm", "base")
	base := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))

	writeFile(t, root, "internal/example.go", "package example\nconst New = 2\nconst Added = 3\n")
	writeFile(t, root, "internal/example_test.go", "package example\nfunc TestNew() {}\n")
	writeFile(t, root, "migrations/system/000001.sql", "select 1;\n")
	writeFile(t, root, "ui/admin/dist/index.js", "new generated\nextra generated\n")
	writeFile(t, root, "deployments/app.yml", "new: config\nmore: config\n")
	if err := os.Remove(filepath.Join(root, "docs/guide.md")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")

	got, err := compareToWorktree(root, base)
	if err != nil {
		t.Fatal(err)
	}
	assertDelta(t, "runtime", got.Categories["runtime"], delta{FilesChanged: 1, GrossAdditionsLOC: 2, GrossDeletionsLOC: 1, NetLOC: 1})
	assertDelta(t, "test", got.Categories["test"], delta{FilesChanged: 1, GrossAdditionsLOC: 2, NetLOC: 2})
	assertDelta(t, "support", got.Categories["support"], delta{FilesChanged: 1, GrossAdditionsLOC: 1, NetLOC: 1})
	assertDelta(t, "generated", got.Categories["generated"], delta{FilesChanged: 1, GrossAdditionsLOC: 2, GrossDeletionsLOC: 1, NetLOC: 1})
	assertDelta(t, "docs", got.Categories["docs"], delta{FilesChanged: 1, GrossDeletionsLOC: 1, NetLOC: -1})
	assertDelta(t, "config", got.Categories["config"], delta{FilesChanged: 1, GrossAdditionsLOC: 2, GrossDeletionsLOC: 1, NetLOC: 1})
	assertDelta(t, "total", got.Total, delta{FilesChanged: 6, GrossAdditionsLOC: 9, GrossDeletionsLOC: 4, NetLOC: 5})
}

func TestCompareToWorktreeReconcilesNewlineLOCAndSharedCategories(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	writeFile(t, root, "internal/testutil/helper.go", "package testutil\n")
	writeFile(t, root, "internal/no_newline.go", "package internal\n")
	writeFile(t, root, "ui/admin/src/example.spec.ts", "export const oldValue = 1\n")
	writeFile(t, root, "binary.dat", "\x00old\n")
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=R0 Test", "-c", "user.email=r0@example.invalid", "commit", "-qm", "base")
	base := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))

	writeFile(t, root, "internal/testutil/helper.go", "package testutil\nconst Added = true\n")
	writeFile(t, root, "internal/no_newline.go", "package internal\nconst Changed = true")
	writeFile(t, root, "ui/admin/src/example.spec.ts", "export const oldValue = 1\nexport const newValue = 2\n")
	writeFile(t, root, "binary.dat", "\x00new\nsecond\n")
	git(t, root, "add", ".")

	got, err := compareToWorktree(root, base)
	if err != nil {
		t.Fatal(err)
	}
	assertDelta(t, "runtime", got.Categories["runtime"], delta{FilesChanged: 2, GrossAdditionsLOC: 1, NetLOC: 1})
	assertDelta(t, "test", got.Categories["test"], delta{FilesChanged: 1, GrossAdditionsLOC: 1, NetLOC: 1})
	assertDelta(t, "config binary", got.Categories["config"], delta{FilesChanged: 1, GrossAdditionsLOC: 1, NetLOC: 1})
	assertDelta(t, "total", got.Total, delta{FilesChanged: 4, GrossAdditionsLOC: 3, NetLOC: 3})

	files, err := worktreeFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	measured, err := analyze(files)
	if err != nil {
		t.Fatal(err)
	}
	assertCount(t, "shared Go runtime category", measured.Runtime.Go, count{Files: 2, LOC: 3})
	assertCount(t, "spec test category", measured.Tests.Playwright, count{Files: 1, LOC: 2})
}

func TestCompareToWorktreeDocumentsNoRenameAccounting(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	writeFile(t, root, "docs/old.md", "package renamed\n")
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=R0 Test", "-c", "user.email=r0@example.invalid", "commit", "-qm", "base")
	base := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "docs", "old.md"), filepath.Join(root, "internal", "renamed.go")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")

	got, err := compareToWorktree(root, base)
	if err != nil {
		t.Fatal(err)
	}
	assertDelta(t, "rename source", got.Categories["docs"], delta{FilesChanged: 1, GrossDeletionsLOC: 1, NetLOC: -1})
	assertDelta(t, "rename target", got.Categories["runtime"], delta{FilesChanged: 1, GrossAdditionsLOC: 1, NetLOC: 1})
	assertDelta(t, "rename total", got.Total, delta{FilesChanged: 2, GrossAdditionsLOC: 1, GrossDeletionsLOC: 1})
}

func TestCommandReadsSkipWorktreeFilesFromTheIndex(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	writeFile(t, root, "keep/a.go", "package keep\n")
	writeFile(t, root, "omit/b.go", "package omit\n")
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=R0 Test", "-c", "user.email=r0@example.invalid", "commit", "-qm", "base")
	git(t, root, "sparse-checkout", "init", "--cone")
	git(t, root, "sparse-checkout", "set", "keep")

	var output bytes.Buffer
	if err := run([]string{"--root", root}, &output); err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output.String())
	}
	assertCount(t, "sparse tracked total", got.Metrics.TotalTracked, count{Files: 2, LOC: 2})
	assertCount(t, "sparse runtime", got.Metrics.Runtime.Go, count{Files: 2, LOC: 2})
}

func TestCommandMeasuresOnlyTrackedWorktreeFiles(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	writeFile(t, root, "tracked.go", "package tracked\n")
	git(t, root, "add", "tracked.go")
	writeFile(t, root, "untracked.go", "package untracked\nconst ignored = true\n")

	var output bytes.Buffer
	if err := run([]string{"--root", root}, &output); err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output.String())
	}
	if got.Source.Kind != "working_tree" || !got.Source.TrackedFilesOnly {
		t.Fatalf("source = %#v", got.Source)
	}
	assertCount(t, "tracked total", got.Metrics.TotalTracked, count{Files: 1, LOC: 1})
	assertCount(t, "tracked runtime", got.Metrics.Runtime.Go, count{Files: 1, LOC: 1})
}

func TestCheckedInBaselineRecordsAuthoritativeCountsAndCommentDiscrepancy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "r0-baseline-0fdc66c2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Revision string `json:"revision"`
		Plan     struct {
			SHA256 string `json:"sha256"`
		} `json:"authoritative_plan"`
		Measurement   report `json:"measurement"`
		Discrepancies []struct {
			Metric string `json:"metric"`
		} `json:"discrepancies"`
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Revision != "0fdc66c2fe930951b1934ad4f7ab20288109bf22" {
		t.Fatalf("baseline revision = %q", artifact.Revision)
	}
	if artifact.Plan.SHA256 != "e2a0e92f51c1249505b6d60cc2956f2b480dfaf7404aaf2cb5a7cc95548adeb9" {
		t.Fatalf("plan SHA-256 = %q", artifact.Plan.SHA256)
	}
	if artifact.Measurement.Definitions != metricDefinitions() {
		t.Fatalf("baseline definitions differ from command definitions")
	}
	metrics := artifact.Measurement.Metrics
	assertCount(t, "baseline total", metrics.TotalTracked, count{Files: 253, LOC: 62572})
	assertCount(t, "baseline Go runtime", metrics.Runtime.Go, count{Files: 86, LOC: 20072})
	assertCount(t, "baseline UI runtime", metrics.Runtime.UI, count{Files: 19, LOC: 2412})
	assertCount(t, "baseline handwritten runtime", metrics.Runtime.Handwritten, count{Files: 105, LOC: 22484})
	assertCount(t, "baseline Go tests", metrics.Tests.Go, count{Files: 81, LOC: 27994})
	assertCount(t, "baseline Vitest", metrics.Tests.Vitest, count{Files: 12, LOC: 2500})
	assertCount(t, "baseline Playwright", metrics.Tests.Playwright, count{Files: 1, LOC: 19})
	assertCount(t, "baseline docs", metrics.Docs, count{Files: 18, LOC: 5187})
	if metrics.ProductionGo != (productionGoMetrics{InterfaceDeclarations: 19, ExportedDeclarations: 337, NewConstructors: 24}) {
		t.Fatalf("baseline production Go = %#v", metrics.ProductionGo)
	}
	if metrics.Comments.GoRuntime != 844 || metrics.Comments.UIRuntime != 30 || metrics.Comments.UIAllSource != 41 || metrics.Comments.UIVitestCommentLike != 11 || metrics.Comments.UILegacyRegexAllSource != 42 {
		t.Fatalf("baseline comments = %#v", metrics.Comments)
	}
	if metrics.UI != (uiMetrics{PageDirectStateEffectOccurrences: 69, PageDirectUseStateOccurrences: 58, PageDirectUseEffectOccurrences: 11, RuntimeExports: 83}) {
		t.Fatalf("baseline UI structure = %#v", metrics.UI)
	}
	if len(metrics.LongProductionFiles) != 3 {
		t.Fatalf("baseline long files = %#v", metrics.LongProductionFiles)
	}
	if len(artifact.Discrepancies) != 1 || artifact.Discrepancies[0].Metric != "comments.ui_runtime_comment_like_lines" {
		t.Fatalf("baseline discrepancies = %#v", artifact.Discrepancies)
	}
}

func assertCount(t *testing.T, name string, got, want count) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func assertDelta(t *testing.T, name string, got, want delta) {
	t.Helper()
	if got != want {
		t.Fatalf("%s delta = %#v, want %#v", name, got, want)
	}
}

func writeFile(t *testing.T, root, path, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
