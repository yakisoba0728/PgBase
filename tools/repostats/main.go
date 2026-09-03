package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const schemaVersion = 2

type fileSnapshot struct {
	Path string
	Data []byte
}

type count struct {
	Files int `json:"files"`
	LOC   int `json:"loc"`
}

type source struct {
	Kind             string `json:"kind"`
	Revision         string `json:"revision,omitempty"`
	TrackedFilesOnly bool   `json:"tracked_files_only"`
}

type definitions struct {
	LOC                  string `json:"loc"`
	GoRuntime            string `json:"go_runtime"`
	UIRuntime            string `json:"ui_runtime"`
	GoTests              string `json:"go_tests"`
	Vitest               string `json:"vitest"`
	Playwright           string `json:"playwright"`
	Docs                 string `json:"docs"`
	ExportedGo           string `json:"exported_go_declarations"`
	Constructors         string `json:"constructors"`
	GoComments           string `json:"go_comments"`
	UIComments           string `json:"ui_comments"`
	PageHooks            string `json:"page_hooks"`
	UIExports            string `json:"ui_exports"`
	LongProductionFiles  string `json:"long_production_files"`
	ComparisonCategories string `json:"comparison_categories"`
}

type runtimeMetrics struct {
	Go          count `json:"go"`
	UI          count `json:"ui"`
	Handwritten count `json:"handwritten_total"`
}

type testMetrics struct {
	Go         count `json:"go"`
	Vitest     count `json:"vitest"`
	Playwright count `json:"playwright"`
}

type productionGoMetrics struct {
	InterfaceDeclarations int `json:"interface_declarations"`
	ExportedDeclarations  int `json:"exported_type_function_method_declarations"`
	NewConstructors       int `json:"new_constructors"`
}

type commentMetrics struct {
	GoRuntime                       int `json:"go_runtime_comment_only_lines"`
	UIRuntime                       int `json:"ui_runtime_comment_like_lines"`
	UIAllSource                     int `json:"ui_all_source_comment_like_lines"`
	UIExcludedNonRuntimeCommentLike int `json:"ui_excluded_non_runtime_comment_like_lines"`
	UIVitestCommentLike             int `json:"ui_vitest_comment_like_lines"`
	UILegacyRegexAllSource          int `json:"ui_legacy_regex_all_source_lines"`
}

type longFile struct {
	Path string `json:"path"`
	LOC  int    `json:"loc"`
}

type uiMetrics struct {
	PageDirectStateEffectOccurrences int `json:"page_direct_use_state_effect_occurrences"`
	PageDirectUseStateOccurrences    int `json:"page_direct_use_state_occurrences"`
	PageDirectUseEffectOccurrences   int `json:"page_direct_use_effect_occurrences"`
	RuntimeExports                   int `json:"runtime_exports"`
}

type metrics struct {
	TotalTracked        count               `json:"total_tracked"`
	Runtime             runtimeMetrics      `json:"runtime"`
	Tests               testMetrics         `json:"tests"`
	Docs                count               `json:"docs"`
	ProductionGo        productionGoMetrics `json:"production_go"`
	Comments            commentMetrics      `json:"comments"`
	LongProductionFiles []longFile          `json:"production_files_over_800_loc"`
	UI                  uiMetrics           `json:"ui"`
}

type delta struct {
	FilesChanged      int `json:"files_changed"`
	GrossAdditionsLOC int `json:"gross_additions_loc"`
	GrossDeletionsLOC int `json:"gross_deletions_loc"`
	NetLOC            int `json:"net_loc"`
}

type comparison struct {
	BaseRevision string           `json:"base_revision"`
	Target       string           `json:"target"`
	Categories   map[string]delta `json:"categories"`
	Total        delta            `json:"total"`
}

type report struct {
	SchemaVersion int         `json:"schema_version"`
	Source        source      `json:"source"`
	Definitions   definitions `json:"definitions"`
	Metrics       metrics     `json:"metrics"`
	Comparison    *comparison `json:"comparison,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("repostats", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	revision := flags.String("rev", "", "measure a committed revision instead of the working tree")
	compareBase := flags.String("compare", "", "compare this base revision to the tracked working tree")
	rootFlag := flags.String("root", "", "repository root (defaults to the current repository)")
	pretty := flags.Bool("pretty", false, "indent JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *revision != "" && *compareBase != "" {
		return errors.New("--rev and --compare are mutually exclusive")
	}

	root, err := repositoryRoot(*rootFlag)
	if err != nil {
		return err
	}

	var files []fileSnapshot
	var reportSource source
	if *revision != "" {
		resolved, resolveErr := resolveRevision(root, *revision)
		if resolveErr != nil {
			return resolveErr
		}
		files, err = revisionFiles(root, resolved)
		reportSource = source{Kind: "revision", Revision: resolved, TrackedFilesOnly: true}
	} else {
		files, err = worktreeFiles(root)
		reportSource = source{Kind: "working_tree", TrackedFilesOnly: true}
	}
	if err != nil {
		return err
	}

	measured, err := analyze(files)
	if err != nil {
		return err
	}
	out := report{
		SchemaVersion: schemaVersion,
		Source:        reportSource,
		Definitions:   metricDefinitions(),
		Metrics:       measured,
	}
	if *compareBase != "" {
		out.Comparison, err = compareToWorktree(root, *compareBase)
		if err != nil {
			return err
		}
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if *pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(out)
}

func repositoryRoot(explicit string) (string, error) {
	if explicit != "" {
		absolute, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("resolve repository root: %w", err)
		}
		output, err := gitOutput(absolute, "rev-parse", "--show-toplevel")
		if err != nil {
			return "", fmt.Errorf("%s is not a Git repository: %w", absolute, err)
		}
		return strings.TrimSpace(string(output)), nil
	}
	output, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find repository root: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveRevision(root, revision string) (string, error) {
	output, err := gitOutput(root, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve revision %q: %w", revision, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func revisionFiles(root, revision string) ([]fileSnapshot, error) {
	output, err := gitOutput(root, "ls-tree", "-r", "--name-only", "-z", revision)
	if err != nil {
		return nil, fmt.Errorf("list revision %s: %w", revision, err)
	}
	paths := splitNUL(output)
	files := make([]fileSnapshot, 0, len(paths))
	for _, path := range paths {
		data, showErr := gitOutput(root, "show", revision+":"+path)
		if showErr != nil {
			return nil, fmt.Errorf("read %s at %s: %w", path, revision, showErr)
		}
		files = append(files, fileSnapshot{Path: filepath.ToSlash(path), Data: data})
	}
	return files, nil
}

func worktreeFiles(root string) ([]fileSnapshot, error) {
	output, err := gitOutput(root, "ls-files", "-t", "-z")
	if err != nil {
		return nil, fmt.Errorf("list tracked working-tree files: %w", err)
	}
	entries := splitNUL(output)
	files := make([]fileSnapshot, 0, len(entries))
	for _, entry := range entries {
		if len(entry) < 3 || entry[1] != ' ' {
			return nil, fmt.Errorf("parse tracked working-tree entry %q", entry)
		}
		tag, path := entry[0], entry[2:]
		absolute := filepath.Join(root, filepath.FromSlash(path))
		info, statErr := os.Lstat(absolute)
		if errors.Is(statErr, os.ErrNotExist) {
			if tag == 'S' {
				data, readErr := gitOutput(root, "show", ":"+path)
				if readErr != nil {
					return nil, fmt.Errorf("read skip-worktree file %s from index: %w", path, readErr)
				}
				files = append(files, fileSnapshot{Path: filepath.ToSlash(path), Data: data})
			}
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("stat tracked file %s: %w", path, statErr)
		}
		var data []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(absolute)
			if readErr != nil {
				return nil, fmt.Errorf("read tracked symlink %s: %w", path, readErr)
			}
			data = []byte(target)
		} else {
			data, statErr = os.ReadFile(absolute)
			if statErr != nil {
				return nil, fmt.Errorf("read tracked file %s: %w", path, statErr)
			}
		}
		files = append(files, fileSnapshot{Path: filepath.ToSlash(path), Data: data})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func analyze(files []fileSnapshot) (metrics, error) {
	var result metrics
	for _, file := range files {
		loc := lineCount(file.Data)
		result.TotalTracked.Files++
		result.TotalTracked.LOC += loc

		switch {
		case isGoTest(file.Path):
			result.Tests.Go.Files++
			result.Tests.Go.LOC += loc
		case isGoRuntime(file.Path):
			result.Runtime.Go.Files++
			result.Runtime.Go.LOC += loc
			goMetrics, err := inspectGo(file)
			if err != nil {
				return metrics{}, err
			}
			result.ProductionGo.InterfaceDeclarations += goMetrics.InterfaceDeclarations
			result.ProductionGo.ExportedDeclarations += goMetrics.ExportedDeclarations
			result.ProductionGo.NewConstructors += goMetrics.NewConstructors
			result.Comments.GoRuntime += countGoCommentOnlyLines(file.Data)
		}

		if isUIRuntime(file.Path) {
			result.Runtime.UI.Files++
			result.Runtime.UI.LOC += loc
			result.Comments.UIRuntime += countUICommentLikeLines(file.Data)
			result.UI.RuntimeExports += countUIExports(file.Data)
			if isUIPage(file.Path) {
				stateCalls, effectCalls := countDirectPageHooks(file.Data)
				result.UI.PageDirectUseStateOccurrences += stateCalls
				result.UI.PageDirectUseEffectOccurrences += effectCalls
				result.UI.PageDirectStateEffectOccurrences += stateCalls + effectCalls
			}
		}
		if isVitest(file.Path) {
			result.Tests.Vitest.Files++
			result.Tests.Vitest.LOC += loc
			result.Comments.UIVitestCommentLike += countUICommentLikeLines(file.Data)
		}
		if isPlaywright(file.Path) {
			result.Tests.Playwright.Files++
			result.Tests.Playwright.LOC += loc
		}
		if isUISource(file.Path) {
			result.Comments.UIAllSource += countUICommentLikeLines(file.Data)
			result.Comments.UILegacyRegexAllSource += countUILegacyRegexLines(file.Data)
		}
		if isDocs(file.Path) {
			result.Docs.Files++
			result.Docs.LOC += loc
		}
		if (isGoRuntime(file.Path) || isUIRuntime(file.Path)) && loc > 800 {
			result.LongProductionFiles = append(result.LongProductionFiles, longFile{Path: file.Path, LOC: loc})
		}
	}
	result.Runtime.Handwritten = count{
		Files: result.Runtime.Go.Files + result.Runtime.UI.Files,
		LOC:   result.Runtime.Go.LOC + result.Runtime.UI.LOC,
	}
	result.Comments.UIExcludedNonRuntimeCommentLike = result.Comments.UIAllSource - result.Comments.UIRuntime
	sort.Slice(result.LongProductionFiles, func(i, j int) bool {
		if result.LongProductionFiles[i].LOC == result.LongProductionFiles[j].LOC {
			return result.LongProductionFiles[i].Path < result.LongProductionFiles[j].Path
		}
		return result.LongProductionFiles[i].LOC > result.LongProductionFiles[j].LOC
	})
	return result, nil
}

func inspectGo(file fileSnapshot) (productionGoMetrics, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Data, 0)
	if err != nil {
		return productionGoMetrics{}, fmt.Errorf("parse production Go file %s: %w", file.Path, err)
	}
	var result productionGoMetrics
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			if declaration.Tok != token.TYPE {
				continue
			}
			for _, spec := range declaration.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, ok := typeSpec.Type.(*ast.InterfaceType); ok {
					result.InterfaceDeclarations++
				}
				if ast.IsExported(typeSpec.Name.Name) {
					result.ExportedDeclarations++
				}
			}
		case *ast.FuncDecl:
			if ast.IsExported(declaration.Name.Name) {
				result.ExportedDeclarations++
			}
			if declaration.Recv == nil && strings.HasPrefix(declaration.Name.Name, "New") {
				result.NewConstructors++
			}
		}
	}
	return result, nil
}

func compareToWorktree(root, base string) (*comparison, error) {
	resolved, err := resolveRevision(root, base)
	if err != nil {
		return nil, err
	}
	output, err := gitOutput(root, "diff", "--numstat", "--no-renames", "-z", resolved, "--")
	if err != nil {
		return nil, fmt.Errorf("compare %s to working tree: %w", resolved, err)
	}
	baseFiles, err := revisionFiles(root, resolved)
	if err != nil {
		return nil, err
	}
	currentFiles, err := worktreeFiles(root)
	if err != nil {
		return nil, err
	}
	baseByPath := snapshotsByPath(baseFiles)
	currentByPath := snapshotsByPath(currentFiles)
	categories := map[string]delta{
		"runtime":   {},
		"test":      {},
		"support":   {},
		"generated": {},
		"docs":      {},
		"config":    {},
	}
	var total delta
	for _, entry := range splitNUL(output) {
		fields := strings.SplitN(entry, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("parse git numstat entry %q", entry)
		}
		path := filepath.ToSlash(fields[2])
		oldLOC := lineCount(baseByPath[path])
		newLOC := lineCount(currentByPath[path])
		var additions, deletions int
		if fields[0] == "-" || fields[1] == "-" {
			if newLOC > oldLOC {
				additions = newLOC - oldLOC
			} else {
				deletions = oldLOC - newLOC
			}
		} else {
			var addErr, deleteErr error
			additions, addErr = strconv.Atoi(fields[0])
			deletions, deleteErr = strconv.Atoi(fields[1])
			if addErr != nil || deleteErr != nil {
				return nil, fmt.Errorf("parse git numstat counts for %s", fields[2])
			}
			additions, deletions = reconcileLineDelta(additions, deletions, oldLOC, newLOC)
		}
		category := comparisonCategory(path)
		categoryDelta := categories[category]
		categoryDelta.FilesChanged++
		categoryDelta.GrossAdditionsLOC += additions
		categoryDelta.GrossDeletionsLOC += deletions
		categoryDelta.NetLOC = categoryDelta.GrossAdditionsLOC - categoryDelta.GrossDeletionsLOC
		categories[category] = categoryDelta
		total.FilesChanged++
		total.GrossAdditionsLOC += additions
		total.GrossDeletionsLOC += deletions
	}
	total.NetLOC = total.GrossAdditionsLOC - total.GrossDeletionsLOC
	return &comparison{BaseRevision: resolved, Target: "working_tree", Categories: categories, Total: total}, nil
}

func snapshotsByPath(files []fileSnapshot) map[string][]byte {
	result := make(map[string][]byte, len(files))
	for _, file := range files {
		result[file.Path] = file.Data
	}
	return result
}

func reconcileLineDelta(additions, deletions, oldLOC, newLOC int) (int, int) {
	desiredNet := newLOC - oldLOC
	actualNet := additions - deletions
	if actualNet > desiredNet {
		difference := actualNet - desiredNet
		reduction := min(additions, difference)
		additions -= reduction
		deletions += difference - reduction
	} else if actualNet < desiredNet {
		difference := desiredNet - actualNet
		reduction := min(deletions, difference)
		deletions -= reduction
		additions += difference - reduction
	}
	return additions, deletions
}

func comparisonCategory(path string) string {
	switch {
	case strings.HasPrefix(path, "ui/admin/dist/"):
		return "generated"
	case isGoTest(path), isVitest(path), isPlaywright(path):
		return "test"
	case strings.HasPrefix(path, "tools/"),
		strings.HasPrefix(path, "scripts/"),
		strings.HasPrefix(path, "ui/admin/src/test/"),
		strings.HasPrefix(path, "migrations/"):
		return "support"
	case isDocs(path), path == "README.md", path == "SECURITY.md":
		return "docs"
	case isGoRuntime(path), isUIRuntime(path):
		return "runtime"
	default:
		return "config"
	}
}

func metricDefinitions() definitions {
	return definitions{
		LOC:                  "number of newline bytes, matching wc -l; binary tracked blobs are included in total LOC",
		GoRuntime:            "tracked .go files excluding *_test.go and tools/**; this authoritative-compatible mechanical set includes internal/testutil",
		UIRuntime:            "ui/admin/src/**/*.{ts,tsx,css}, excluding *.test.{ts,tsx} and ui/admin/src/test/**; CSS is included",
		GoTests:              "tracked *_test.go files",
		Vitest:               "tracked ui/admin/src/**/*.test.{ts,tsx} files",
		Playwright:           "tracked ui/admin/**/*.spec.{ts,tsx} files",
		Docs:                 "tracked files under docs/**",
		ExportedGo:           "exported type declarations plus exported free-function and method declarations in the Go runtime file set, counted with go/ast",
		Constructors:         "free-function declarations whose names begin with New in the Go runtime file set",
		GoComments:           "Go runtime lines whose trimmed text begins with //",
		UIComments:           "comment-only lines beginning with //, /*, or {/* plus continuation lines inside a block comment; runtime, all-source, and legacy-regex totals are reported separately",
		PageHooks:            "direct useState/useEffect call tokens outside comments and strings in ui/admin/src/pages/** runtime files",
		UIExports:            "UI runtime top-level export statement lines, not the number of individually exported symbols",
		LongProductionFiles:  ">800 newline bytes in a Go or UI runtime file",
		ComparisonCategories: "runtime=the authoritative-compatible Go/UI runtime sets (including internal/testutil); test=Go/Vitest/Playwright; support=tools/scripts/UI test helpers/migrations; generated=embedded UI dist; docs=docs/** plus root README/SECURITY; config=remaining tracked files; exact renames are counted as one deletion plus one addition so category movement remains visible",
	}
}

func lineCount(data []byte) int {
	return bytes.Count(data, []byte{'\n'})
}

func countGoCommentOnlyLines(data []byte) int {
	return countLines(data, func(trimmed string) bool { return strings.HasPrefix(trimmed, "//") })
}

func countUICommentLikeLines(data []byte) int {
	count := 0
	inBlock := false
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		trimmed := strings.TrimSpace(string(line))
		if inBlock {
			count++
			if strings.Contains(trimmed, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			count++
			continue
		}
		if strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "{/*") {
			count++
			if !strings.Contains(trimmed, "*/") {
				inBlock = true
			}
		}
	}
	return count
}

func countUILegacyRegexLines(data []byte) int {
	return countLines(data, func(trimmed string) bool {
		return strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "*/") ||
			strings.HasPrefix(trimmed, "{/*")
	})
}

func countUIExports(data []byte) int {
	return countLines(data, func(trimmed string) bool {
		return trimmed == "export" || strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "export{")
	})
}

func countDirectPageHooks(data []byte) (int, int) {
	code := jsCodeOnly(data)
	stateCalls := countJSCalls(code, "useState")
	effectCalls := countJSCalls(code, "useEffect")
	return stateCalls, effectCalls
}

func countJSCalls(code []byte, name string) int {
	count := 0
	needle := []byte(name)
	for offset := 0; offset < len(code); {
		index := bytes.Index(code[offset:], needle)
		if index < 0 {
			break
		}
		index += offset
		end := index + len(needle)
		if (index == 0 || !isJSIdentifierByte(code[index-1])) &&
			(end == len(code) || !isJSIdentifierByte(code[end])) &&
			(index == 0 || code[index-1] != '.') && isCallSuffix(code, end) {
			count++
		}
		offset = end
	}
	return count
}

func isCallSuffix(code []byte, offset int) bool {
	offset = skipSpace(code, offset)
	if offset < len(code) && code[offset] == '<' {
		depth := 0
		for ; offset < len(code); offset++ {
			switch code[offset] {
			case '<':
				depth++
			case '>':
				depth--
				if depth == 0 {
					offset++
					offset = skipSpace(code, offset)
					return offset < len(code) && code[offset] == '('
				}
			}
		}
		return false
	}
	return offset < len(code) && code[offset] == '('
}

func skipSpace(data []byte, offset int) int {
	for offset < len(data) {
		switch data[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func isJSIdentifierByte(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func jsCodeOnly(data []byte) []byte {
	const (
		jsNormal = iota
		jsLineComment
		jsBlockComment
		jsSingleQuote
		jsDoubleQuote
		jsTemplate
	)
	result := append([]byte(nil), data...)
	state := jsNormal
	escaped := false
	for index := 0; index < len(result); index++ {
		current := result[index]
		next := byte(0)
		if index+1 < len(result) {
			next = result[index+1]
		}
		switch state {
		case jsNormal:
			switch {
			case current == '/' && next == '/':
				result[index], result[index+1] = ' ', ' '
				index++
				state = jsLineComment
			case current == '/' && next == '*':
				result[index], result[index+1] = ' ', ' '
				index++
				state = jsBlockComment
			case current == '\'':
				result[index] = ' '
				state = jsSingleQuote
				escaped = false
			case current == '"':
				result[index] = ' '
				state = jsDoubleQuote
				escaped = false
			case current == '`':
				result[index] = ' '
				state = jsTemplate
				escaped = false
			}
		case jsLineComment:
			if current == '\n' {
				state = jsNormal
			} else {
				result[index] = ' '
			}
		case jsBlockComment:
			if current == '*' && next == '/' {
				result[index], result[index+1] = ' ', ' '
				index++
				state = jsNormal
			} else if current != '\n' {
				result[index] = ' '
			}
		case jsSingleQuote, jsDoubleQuote, jsTemplate:
			quote := byte('\'')
			if state == jsDoubleQuote {
				quote = '"'
			} else if state == jsTemplate {
				quote = '`'
			}
			if current == '\n' && state != jsTemplate {
				state = jsNormal
				escaped = false
				continue
			}
			result[index] = ' '
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == quote {
				state = jsNormal
			}
		}
	}
	return result
}

func countLines(data []byte, predicate func(string) bool) int {
	count := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if predicate(strings.TrimSpace(string(line))) {
			count++
		}
	}
	return count
}

func isGoTest(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

func isGoRuntime(path string) bool {
	return strings.HasSuffix(path, ".go") && !isGoTest(path) && !strings.HasPrefix(path, "tools/")
}

func isUISource(path string) bool {
	if !strings.HasPrefix(path, "ui/admin/src/") {
		return false
	}
	return hasAnySuffix(path, ".ts", ".tsx", ".css")
}

func isUIRuntime(path string) bool {
	return isUISource(path) && !isVitest(path) && !isPlaywright(path) && !strings.HasPrefix(path, "ui/admin/src/test/")
}

func isUIPage(path string) bool {
	return isUIRuntime(path) && strings.HasPrefix(path, "ui/admin/src/pages/")
}

func isVitest(path string) bool {
	return strings.HasPrefix(path, "ui/admin/src/") && hasAnySuffix(path, ".test.ts", ".test.tsx")
}

func isUITestSpec(path string) bool {
	return strings.HasPrefix(path, "ui/admin/") && hasAnySuffix(path, ".spec.ts", ".spec.tsx")
}

func isPlaywright(path string) bool { return isUITestSpec(path) }

func isDocs(path string) bool {
	return strings.HasPrefix(path, "docs/")
}

func hasAnySuffix(value string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func splitNUL(data []byte) []string {
	parts := bytes.Split(data, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			result = append(result, string(part))
		}
	}
	return result
}

func gitOutput(root string, args ...string) ([]byte, error) {
	commandArgs := args
	if root != "" {
		commandArgs = append([]string{"-C", root}, args...)
	}
	command := exec.Command("git", commandArgs...)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSpace(string(exitErr.Stderr))
		if message != "" {
			return nil, errors.New(message)
		}
	}
	return nil, err
}
