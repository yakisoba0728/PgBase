package repositorypolicy

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The data group the plan budgets as one number. Per-package ratchets live with
// their packages; nothing enforced the group, so it could grow by moving lines
// between these five directories without any test noticing.
//
// R11 fixed the budget from the measurement, as decision D1 directs: expected
// 16,100-16,500 with a hard limit of 16,750, set once from the real
// gross-new/gross-deleted totals and never raised again. The measured value at
// the end of R11 is the limit below, 97 lines under that ceiling.
//
// The plan's original 12,750 predates surfaces the plan itself then required:
// the R2b operation ledger, the R4 membership sidecar and rollout, the R5
// companion-index capability, and the R6 cascade batching that removed the
// delete N+1 at a net +86. Those are correctness and migration stacks, which
// plan section 11 allows to be LOC-positive.
//
// Raising this number is allowed only in the same stack as the deletion that
// pays for it, with the gross-new/gross-deleted accounting written here.
// Lowered from 16,653 by audit finding STD-2, which paid for the per-version
// delete graph by deleting Cache.Replace and the duplication around it. A
// zero-slack ratchet tracks the measurement in both directions; only raising
// it needs the accounting recorded below.
const dataGroupRuntimeLimit = 16_645

var dataGroupPackages = []string{
	"internal/database",
	"internal/query",
	"internal/recordops",
	"internal/relation",
	"internal/schema",
}

func TestDataGroupRuntimeStaysWithinItsBudget(t *testing.T) {
	root := filepath.Join("..", "..")
	total := 0
	perPackage := make(map[string]int, len(dataGroupPackages))
	for _, name := range dataGroupPackages {
		lines := 0
		entries, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				// None of the five has a subpackage today. If one appears, its
				// lines belong to the group and this test has to walk it.
				t.Fatalf("%s has subdirectory %s, which this budget does not count", name, entry.Name())
			}
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			source, err := os.ReadFile(filepath.Join(root, name, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			lines += bytes.Count(source, []byte{'\n'})
		}
		perPackage[name] = lines
		total += lines
	}
	if total > dataGroupRuntimeLimit {
		t.Fatalf("data group runtime lines = %d, hard limit %d: %v", total, dataGroupRuntimeLimit, perPackage)
	}
}

// The structural budgets the plan tracks repository-wide. They are read from
// tools/repostats so this test cannot disagree with the report about what an
// exported declaration or a runtime file is.
//
// repostats measures Git-tracked files. A new runtime file that is not staged
// is invisible here and will trip the gate on a clean checkout instead, which
// is the direction that fails safe.
//
// Every limit is the value measured at the end of R11, with no slack. The
// plan's targets are recorded next to each one; where the measurement is above
// the target, R11's re-forecast and its reason are in
// docs/superpowers/plans/2026-09-02-open-decisions.md.
//
// Deliberately absent: the Go runtime comment-only line count. It is the one
// metric whose ratchet would push in the direction plan section 12 forbids,
// since the cheapest way to satisfy it is to delete the concurrency and
// security rationale section 5 requires be kept. repostats reports it and the
// handoff records the audited floor; it is an indicator, not a gate.
//
// Two limits were raised once after R11, for audit finding SPEC-1: the
// record-membership rollout transitions had no production caller, so a database
// that already held records could not be carried to the state Release C's read
// path admits and failed every list closed. The fix is the `pgbase membership`
// operator command decision D2-2 requires, which is necessarily new surface.
//
//	handwritten runtime lines       34,812 -> 35,109
//	  gross-new 298 / gross-deleted 1, net +297
//	  internal/cli/membership.go        +252
//	  internal/membership/fleet.go       +44
//	  internal/cli/run.go            +2 / -1
//	production exported declarations   467 -> 468
//	  (*membership.Service).ActiveWriterFleet, the read half of the operator
//	  surface; the transitions themselves were already exported.
//
// Raised again, on the residual RUN-1 left open. The heartbeat's tolerance is a
// promise about wall-clock time, but the window was only re-examined after a
// renewal attempt returned, and an attempt blocks for Options.StatementTimeout,
// which Normalized accepts up to 2147483647ms. An operator who raised it could
// have the loop stop after the lease it was tolerating had already expired.
//
//	handwritten runtime lines      35,109 -> 35,114
//	  gross-new 7 / gross-deleted 0, net +7 over the 35,107 actually measured
//	  after RUN-1 and RUN-4 landed, which paid two lines more than they spent.
//	  internal/membership/runtime.go     +7 (3 code, 4 rationale)
//
// Raised once more, for audit finding STD-1: a draft edited while its apply or
// save was in flight was discarded by the success handler, which released the
// unsaved-draft hold and navigated away against the draft it had sent rather
// than the one on screen. Keeping the newer draft means the page stays, which
// in turn means the data-loss warning has to be re-measured against the
// collection the apply returned -- the server already sends it and the adapter
// was discarding it -- or a column another client can still write to would be
// dropped with no warning shown.
//
//	handwritten runtime lines      35,114 -> 35,150
//	UI runtime lines                3,473 ->  3,517
//	  gross-new 44 / gross-deleted 0 in the UI, less 8 paid in Go by STD-2
//	  ui/admin/src/designer/collection-designer.ts  +24
//	  ui/admin/src/workspace/record-workspace.ts    +10
//	  ui/admin/src/backend/ports.ts                  +5
//	  ui/admin/src/backend/http-backend.ts           +5
//
// No new UI runtime export pays for it (119, unchanged), and the four messages
// the fix has to show are string constants, not surface. Refusing the edit
// instead of preserving it would have been cheaper in lines and is what plan
// section 12 would have bought; F0's criteria and the finding both ask for the
// edit to survive, so this is the direction the budget yields to.
// Nothing paid for it, and that is recorded rather than disguised. `unused` is
// enabled in .golangci.yml and reports zero findings, so there is no dead
// runtime code in either package to delete, and plan section 11 allows a
// correctness and migration stack to be LOC-positive. The alternative deletion
// on the table was internal/testutil/membership.go (110 runtime lines), whose
// SQL shortcut to the retired state exists only as a test fixture — but every
// integration test that lists a record depends on it, and replacing it with the
// real rollout would make those suites drive six transitions each. That is a
// trade to make deliberately in its own stack, not inside a P1 fix.
type structuralLimit struct {
	name    string
	value   func(repositoryMetrics) int
	limit   int
	planned string
}

var structuralLimits = []structuralLimit{
	{"handwritten runtime lines", func(m repositoryMetrics) int { return m.Runtime.Handwritten.LOC }, 35_150, "plan 22,484"},
	{"UI runtime lines", func(m repositoryMetrics) int { return m.Runtime.UI.LOC }, 3_517, "plan 2,050"},
	{"production interface declarations", func(m repositoryMetrics) int { return m.ProductionGo.Interfaces }, 17, "plan 12"},
	{"production exported declarations", func(m repositoryMetrics) int { return m.ProductionGo.Exported }, 468, "plan 285"},
	{"production New* constructors", func(m repositoryMetrics) int { return m.ProductionGo.Constructors }, 25, "plan 16"},
	{"production files over 800 lines", func(m repositoryMetrics) int { return len(m.LongFiles) }, 0, "plan 0"},
	{"UI page direct useState/useEffect", func(m repositoryMetrics) int { return m.UI.PageHooks }, 19, "plan 30"},
	{"UI runtime exports", func(m repositoryMetrics) int { return m.UI.Exports }, 119, "plan 65"},
}

type repositoryMetrics struct {
	Runtime struct {
		UI          struct{ LOC int } `json:"ui"`
		Handwritten struct{ LOC int } `json:"handwritten_total"`
	} `json:"runtime"`
	ProductionGo struct {
		Interfaces   int `json:"interface_declarations"`
		Exported     int `json:"exported_type_function_method_declarations"`
		Constructors int `json:"new_constructors"`
	} `json:"production_go"`
	LongFiles []struct {
		Path string `json:"path"`
	} `json:"production_files_over_800_loc"`
	UI struct {
		PageHooks int `json:"page_direct_use_state_effect_occurrences"`
		Exports   int `json:"runtime_exports"`
	} `json:"ui"`
}

func TestRuntimeStructuralBudgetsDoNotDrift(t *testing.T) {
	root := filepath.Join("..", "..")
	command := exec.Command("go", "run", "./tools/repostats")
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run repostats: %v\n%s", err, stderr.String())
	}
	var report struct {
		Metrics repositoryMetrics `json:"metrics"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	for _, limit := range structuralLimits {
		if got := limit.value(report.Metrics); got > limit.limit {
			t.Errorf("%s = %d, hard limit %d (%s)", limit.name, got, limit.limit, limit.planned)
		}
	}
}
