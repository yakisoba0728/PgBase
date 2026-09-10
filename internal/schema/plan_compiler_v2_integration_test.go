//go:build integration

package schema

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

// applyChange drives the two surfaces that survive STD-3: Preview compiles at
// whatever generation the capability row names, and Apply re-derives the plan
// under the schema lock. Nothing here goes through the Plan*/Apply* wrappers.
func applyChange(t *testing.T, service *Service, change Change, administratorID uuid.UUID) ApplyResult {
	t.Helper()
	ctx := context.Background()
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	result, err := service.Apply(ctx, LegacyApply(change, plan, administratorID))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return result
}

func readyCompanionFixture(t *testing.T) (*database.Pool, *Service, uuid.UUID, Collection) {
	t.Helper()
	pool, service, administratorID, collection := companionRolloutFixture(t)
	activateCompanionRollout(t, service)
	if _, err := service.MaintainCompanionIndexes(context.Background()); err != nil {
		t.Fatalf("maintain companion indexes: %v", err)
	}
	return pool, service, administratorID, collection
}

// companionShapedCollection repeats the fixture's field names so seedCompanionRows
// can fill a collection created during a test.
func companionShapedCollection(name string) CreateCollectionInput {
	return CreateCollectionInput{Name: name, Fields: []FieldInput{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "nickname", Type: TypeText, Indexed: true},
	}}
}

func companionStatus(t *testing.T, service *Service) CompanionIndexStatus {
	t.Helper()
	status, err := service.CompanionIndexStatus(context.Background())
	if err != nil {
		t.Fatalf("read companion index capability: %v", err)
	}
	return status
}

// companionCatalogVersion dereferences the capability's catalog version for a
// failure message, so a wrong one reads as a number and a missing one as -1.
func companionCatalogVersion(status CompanionIndexStatus) int64 {
	if status.CatalogVersion == nil {
		return -1
	}
	return *status.CatalogVersion
}

func currentCatalogVersion(t *testing.T, service *Service) int64 {
	t.Helper()
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	return catalog.Version
}

// SPEC-2's crux. With the capability ready, creating a collection must not cost
// an operator another activate/build cycle: the companion is built in the same
// transaction as the table, the capability follows the catalog it now describes,
// and the page the companion exists for is served from it straight away.
func TestReadyCapabilityCreatesACollectionWithItsCompanionInOneTransaction(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	ctx := context.Background()
	pool, service, administratorID, _ := readyCompanionFixture(t)

	before := companionStatus(t, service)
	if !before.Ready() || before.ReadyIndexCount == nil || *before.ReadyIndexCount != 1 {
		t.Fatalf("capability before the create = %#v", before)
	}

	created := applyChange(t, service,
		NewCreateChange(companionShapedCollection("companion_notes")), administratorID).Collection

	status := companionStatus(t, service)
	version := currentCatalogVersion(t, service)
	if !status.Ready() {
		t.Fatalf("capability after creating a collection = %#v", status)
	}
	if companionCatalogVersion(status) != version {
		t.Fatalf("capability catalog version = %d, want %d", companionCatalogVersion(status), version)
	}
	if status.ReadyIndexCount == nil || *status.ReadyIndexCount != 2 {
		t.Fatalf("promoted companion count = %v, want 2", status.ReadyIndexCount)
	}
	if status.Generation != before.Generation {
		t.Fatalf("generation moved to %d without a new rollout", status.Generation)
	}

	diagnostics := readDiagnostics(t, pool)
	if diagnostics.Expected != 2 || diagnostics.Valid != 2 || diagnostics.Missing != 0 ||
		diagnostics.Invalid != 0 || diagnostics.Mismatched != 0 {
		t.Fatalf("diagnostics after creating a collection = %#v", diagnostics)
	}
	companion := companionSpec(t, service, created, "nickname")
	assertCompanionIndexShape(t, pool, companion)

	// An operator who runs build anyway gets the standing receipt: the capability
	// already describes this catalog, so there is nothing left to do.
	receipt, err := service.MaintainCompanionIndexes(ctx)
	if err != nil {
		t.Fatalf("maintenance after a ready create: %v", err)
	}
	if receipt.State != CompanionIndexReady || receipt.IndexCount != 2 {
		t.Fatalf("maintenance receipt after a ready create = %#v", receipt)
	}

	seedCompanionRows(t, pool, created, 20000)
	page := "select id from " + pgx.Identifier{"app", created.TableName}.Sanitize() + " " +
		fmt.Sprintf(nullableDescendingPageOrderBy, pgx.Identifier{"nickname"}.Sanitize()) + " limit 51"
	if plan := explain(t, pool, page); !strings.Contains(plan, companion.Name) {
		t.Fatalf("new collection's descending page did not use companion %s:\n%s", companion.Name, plan)
	}
}

// P2-C. The gate closes on what the change does to the companion manifest, not on the
// kind of the change. Keying on the kind demoted every update, so a rename or a re-apply
// that names no new companion still cost a full activate-and-build round for adding
// nothing. Both cases here change the catalog -- so this is not "nothing happened" --
// while adding no companion the catalog did not already expect.
func TestAnUpdateThatAddsNoCompanionCarriesTheCapability(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	pool, service, administratorID, collection := readyCompanionFixture(t)
	before := companionStatus(t, service)
	if !before.Ready() {
		t.Fatalf("capability before the updates = %#v", before)
	}

	unchanged := []FieldInput{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "nickname", Type: TypeText, Indexed: true},
	}
	for _, testCase := range []struct {
		name   string
		fields []FieldInput
	}{
		{name: "re-applying the same fields", fields: unchanged},
		// An unindexed column is never sortable, so it names no companion however
		// nullable it is. This is the case that separates the manifest rule from
		// "the catalog did not change".
		{name: "adding an unindexed field", fields: append(append([]FieldInput(nil), unchanged...),
			FieldInput{Name: "memo", Type: TypeText})},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			applyChange(t, service, NewUpdateChange(UpdateCollectionInput{
				Name: collection.Name, Fields: testCase.fields,
			}), administratorID)

			status := companionStatus(t, service)
			if !status.Ready() {
				t.Fatalf("capability after %s = %#v", testCase.name, status)
			}
			// Carrying it means carrying it onto the catalog just committed, or
			// doctor compares a promoted count against a version that moved.
			if version := currentCatalogVersion(t, service); companionCatalogVersion(status) != version {
				t.Fatalf("carried capability catalog version = %d, want %d",
					companionCatalogVersion(status), version)
			}
			if status.ReadyIndexCount == nil || *status.ReadyIndexCount != 1 {
				t.Fatalf("carried companion count = %v, want 1", status.ReadyIndexCount)
			}
			if status.Generation != before.Generation {
				t.Fatalf("generation moved to %d without a new rollout", status.Generation)
			}
			if diagnostics := readDiagnostics(t, pool); diagnostics.Expected != 1 ||
				diagnostics.Valid != 1 || diagnostics.Missing != 0 {
				t.Fatalf("diagnostics after %s = %#v", testCase.name, diagnostics)
			}
		})
	}
}

// P1-B, forward-looking. Migration 9 demotes every capability proved under the
// ambiguous generation once, but the same shape recurs at every generation bump:
// a row proved by any other compiler generation says nothing about this binary.
// State alone cannot express that, so the proved generation is an exact gate.
func TestAReadyCapabilityProvedByAnotherGenerationDoesNotIssueV2(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	ctx := context.Background()
	pool, service, administratorID, _ := readyCompanionFixture(t)
	if _, err := pool.Exec(ctx, `update pgbase_meta.schema_companion_index_capability
		set proven_compiler_version = $1 where singleton`, schemaCompilerVersion-1); err != nil {
		t.Fatal(err)
	}

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	change := NewCreateChange(companionShapedCollection("companion_stale_proof"))
	issued, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatalf("preview under a stale proof: %v", err)
	}
	v1, err := compilePlan(catalog, change, false)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Checksum != v1.Checksum {
		t.Fatalf("a capability proved by an older generation issued %q, want the v1 checksum",
			issued.Checksum)
	}
	// The row still reads 'ready' -- it is the proof behind it that is stale, and
	// that is exactly what a state-only gate could not see.
	if status := companionStatus(t, service); status.State != CompanionIndexReady {
		t.Fatalf("state under a stale proof = %#v", status)
	}
	if _, err := service.Apply(ctx, LegacyApply(change, issued, administratorID)); !errors.Is(err, ErrCompanionIndexFleet) {
		t.Fatalf("apply under a stale generation proof = %v, want ErrCompanionIndexFleet", err)
	}
	if _, err := service.RollbackCompanionIndexes(ctx); err != nil {
		t.Fatalf("roll back stale generation proof: %v", err)
	}
	created := applyChange(t, service, change, administratorID).Collection
	assertCompanionIndexAbsent(t, pool, companionSpec(t, service, created, "nickname"))
	// The operator rollback is what lets v1 commit: a companion-less collection
	// can never be written under a row still claiming companions are required.
	if status := companionStatus(t, service); status.State != CompanionIndexPending {
		t.Fatalf("capability after a v1 create under a stale proof = %#v", status)
	}

	// Re-proving the fleet is what restores v2, and it goes through the ordinary
	// operator path rather than a repair of the row.
	activateCompanionRollout(t, service)
	if _, err := service.MaintainCompanionIndexes(ctx); err != nil {
		t.Fatalf("maintain companion indexes after re-proving: %v", err)
	}
	reproved, err := service.Preview(ctx, NewCreateChange(companionShapedCollection("companion_reproved")))
	if err != nil {
		t.Fatal(err)
	}
	reprovedCatalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := compilePlan(reprovedCatalog, NewCreateChange(companionShapedCollection("companion_reproved")), true)
	if err != nil {
		t.Fatal(err)
	}
	if reproved.Checksum != v2.Checksum {
		t.Fatalf("re-proved capability issued %q, want the v2 checksum", reproved.Checksum)
	}

	if _, err := pool.Exec(ctx, `update pgbase_meta.schema_companion_index_capability
		set proven_compiler_version = $1 where singleton`, schemaCompilerVersion+1); err != nil {
		t.Fatal(err)
	}
	futureChange := NewCreateChange(companionShapedCollection("companion_future_proof"))
	future, err := service.Preview(ctx, futureChange)
	if err != nil {
		t.Fatal(err)
	}
	v1, err = compilePlan(reprovedCatalog, futureChange, false)
	if err != nil {
		t.Fatal(err)
	}
	if future.Checksum != v1.Checksum {
		t.Fatalf("a capability proved by a newer generation issued %q, want the v1 checksum", future.Checksum)
	}
	if _, err := service.Apply(ctx, LegacyApply(futureChange, future, administratorID)); !errors.Is(err, ErrCompanionIndexFleet) {
		t.Fatalf("apply under a newer generation proof = %v, want ErrCompanionIndexFleet", err)
	}
}

// The control for the manifest rule. Adding a nullable indexed field names a companion
// nothing has built, so this update does close the gate and does cost the approved CIC
// maintenance path. Without it the rule above could simply never demote.
func TestUpdatingAnExistingCollectionStillClosesTheCompanionGate(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	ctx := context.Background()
	pool, service, administratorID, collection := readyCompanionFixture(t)

	applyChange(t, service, NewUpdateChange(UpdateCollectionInput{
		Name: collection.Name,
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "nickname", Type: TypeText, Indexed: true},
			{Name: "handle", Type: TypeText, Indexed: true},
		},
	}), administratorID)

	status := companionStatus(t, service)
	if status.Ready() || status.State != CompanionIndexPending || status.CatalogVersion != nil ||
		status.ReadyAt != nil || status.ReadyIndexCount != nil {
		t.Fatalf("capability after adding an indexed nullable field = %#v", status)
	}
	if _, err := service.MaintainCompanionIndexes(ctx); !errors.Is(err, ErrCompanionIndexState) {
		t.Fatalf("maintenance on a closed gate = %v, want ErrCompanionIndexState", err)
	}

	activateCompanionRollout(t, service)
	ready, err := service.MaintainCompanionIndexes(ctx)
	if err != nil {
		t.Fatalf("companion index maintenance: %v", err)
	}
	if ready.State != CompanionIndexReady || ready.IndexCount != 2 || ready.Generation != 2 {
		t.Fatalf("restored rollout receipt = %#v", ready)
	}
	assertCompanionIndexShape(t, pool, companionSpec(t, service, collection, "handle"))
}

// Delete carries, and under the manifest rule it does so for a reason rather than as a
// policy extension: a delete only removes manifest entries, so it can never name a
// companion nothing has built. Carrying the capability means moving the promoted count
// too, because doctor compares that count against the catalog.
func TestDeletingACollectionCarriesTheCompanionCapability(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	pool, service, administratorID, _ := readyCompanionFixture(t)
	created := applyChange(t, service,
		NewCreateChange(companionShapedCollection("companion_notes")), administratorID).Collection

	applyChange(t, service, NewDeleteChange(created.Name), administratorID)

	status := companionStatus(t, service)
	version := currentCatalogVersion(t, service)
	if !status.Ready() || status.ReadyIndexCount == nil || *status.ReadyIndexCount != 1 {
		t.Fatalf("capability after deleting a collection = %#v", status)
	}
	if companionCatalogVersion(status) != version {
		t.Fatalf("capability catalog version = %d, want %d", companionCatalogVersion(status), version)
	}
	if diagnostics := readDiagnostics(t, pool); diagnostics.Expected != 1 ||
		diagnostics.Valid != 1 || diagnostics.Missing != 0 {
		t.Fatalf("diagnostics after deleting a collection = %#v", diagnostics)
	}
}

// R5's mixed-fleet dual validation. The window is not hypothetical even on one
// binary: an activation or rollback can move the capability between a preview
// and its apply, so Apply must accept a checksum from either generation.
func TestApplyAcceptsAPlanChecksumFromEitherCompilerGeneration(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	ctx := context.Background()
	pool, service, administratorID, _ := companionRolloutFixture(t)

	pendingCatalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	change := NewCreateChange(companionShapedCollection("companion_pending_side"))
	pendingIssued, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatalf("preview under a pending capability: %v", err)
	}
	v1, err := compilePlan(pendingCatalog, change, false)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := compilePlan(pendingCatalog, change, true)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Checksum == v2.Checksum {
		t.Fatal("both generations produced one checksum, so accepting either proves nothing")
	}
	if pendingIssued.Checksum != v1.Checksum {
		t.Fatalf("pending capability issued %q, want the v1 checksum", pendingIssued.Checksum)
	}
	pendingResult, err := service.Apply(ctx, LegacyApply(change,
		Plan{Checksum: v2.Checksum, BaseVersion: pendingCatalog.Version}, administratorID))
	if err != nil {
		t.Fatalf("a v1 writer refused a v2 checksum: %v", err)
	}
	// The mirror image, and the half that proves the selection is not simply
	// "always run v1": a pending writer that accepted the v2 checksum must build the
	// companion that statement list names.
	assertCompanionIndexShape(t, pool, companionSpec(t, service, pendingResult.Collection, "nickname"))
	assertMigrationPlanMatchesChecksum(t, pool, currentCatalogVersion(t, service), v2)

	activateCompanionRollout(t, service)
	if _, err := service.MaintainCompanionIndexes(ctx); err != nil {
		t.Fatalf("maintain companion indexes: %v", err)
	}

	readyCatalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second := NewCreateChange(companionShapedCollection("companion_ready_side"))
	readyIssued, err := service.Preview(ctx, second)
	if err != nil {
		t.Fatalf("preview under a ready capability: %v", err)
	}
	readyV1, err := compilePlan(readyCatalog, second, false)
	if err != nil {
		t.Fatal(err)
	}
	readyV2, err := compilePlan(readyCatalog, second, true)
	if err != nil {
		t.Fatal(err)
	}
	if readyIssued.Checksum != readyV2.Checksum {
		t.Fatalf("ready capability issued %q, want the v2 checksum", readyIssued.Checksum)
	}
	if len(readyV2.Statements) != len(readyV1.Statements)+1 {
		t.Fatalf("v2 create plan did not add exactly one companion statement: %v", readyV2.Statements)
	}
	result, err := service.Apply(ctx, LegacyApply(second,
		Plan{Checksum: readyV1.Checksum, BaseVersion: readyCatalog.Version}, administratorID))
	if err != nil {
		t.Fatalf("a v2 writer refused a v1 checksum: %v", err)
	}
	// P1-A. Accepting the checksum is only half the contract: the plan that runs must
	// be the one the caller approved. A v2 writer that ran its own plan here would
	// build a companion index against a caller who agreed to a statement list without
	// one, and record a migration row whose checksum and plan JSON disagree for good.
	assertCompanionIndexAbsent(t, pool, companionSpec(t, service, result.Collection, "nickname"))
	assertMigrationPlanMatchesChecksum(t, pool, currentCatalogVersion(t, service), readyV1)
	// And the capability follows the plan that actually ran, not the row that issued
	// it: a v1 create leaves its companion unbuilt, so the gate closes.
	if status := companionStatus(t, service); status.State != CompanionIndexPending {
		t.Fatalf("capability after executing an approved v1 create = %#v", status)
	}
}

// assertCompanionIndexAbsent is the negative of assertCompanionIndexShape: the index
// the manifest names must not exist at all.
func assertCompanionIndexAbsent(t *testing.T, pool *database.Pool, companion IndexSpec) {
	t.Helper()
	var present bool
	if err := pool.QueryRow(context.Background(),
		"select to_regclass($1) is not null",
		pgx.Identifier{companion.Namespace, companion.Name}.Sanitize()).Scan(&present); err != nil {
		t.Fatalf("probe companion index %s: %v", companion.Name, err)
	}
	if present {
		t.Fatalf("companion index %s exists, but the executed plan never built it", companion.Name)
	}
}

// assertMigrationPlanMatchesChecksum reads back the audit row for one schema version
// and requires its checksum and its recorded plan to describe the same plan. They are
// written from two different values in the apply, so a row that disagrees is permanent
// and silent; this is the only place that compares them.
func assertMigrationPlanMatchesChecksum(t *testing.T, pool *database.Pool, version int64, want Plan) {
	t.Helper()
	var checksum []byte
	var planJSON string
	var statements []string
	if err := pool.QueryRow(context.Background(),
		`select checksum, plan_json::text, sql_statements
		from pgbase_meta.schema_migrations where version = $1`, version).
		Scan(&checksum, &planJSON, &statements); err != nil {
		t.Fatalf("read schema migration audit row %d: %v", version, err)
	}
	if got := hex.EncodeToString(checksum); got != want.Checksum {
		t.Fatalf("migration row checksum = %s, want the approved %s", got, want.Checksum)
	}
	var recorded Plan
	if err := json.Unmarshal([]byte(planJSON), &recorded); err != nil {
		t.Fatalf("decode recorded plan: %v", err)
	}
	if recorded.Checksum != want.Checksum {
		t.Fatalf("recorded plan checksum = %s, but the row's own checksum column says %s",
			recorded.Checksum, want.Checksum)
	}
	if !slices.Equal(statements, want.Statements) {
		t.Fatalf("executed statements = %v, want the approved %v", statements, want.Statements)
	}
	if !slices.Equal(recorded.Statements, want.Statements) {
		t.Fatalf("recorded plan statements = %v, want the approved %v",
			recorded.Statements, want.Statements)
	}
}

// The downgrade half. Rollback is the commitment point in reverse: it returns
// issuance to v1 for every later plan, keeps every index already built, and
// leaves re-activation to prove the fleet again.
func TestCompanionRollbackReturnsPlanIssuanceToV1AndKeepsTheIndex(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	ctx := context.Background()
	pool, service, administratorID, collection := readyCompanionFixture(t)
	companion := companionSpec(t, service, collection, "nickname")

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	change := NewCreateChange(companionShapedCollection("companion_after_rollback"))
	readyPlan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := compilePlan(catalog, change, true)
	if err != nil {
		t.Fatal(err)
	}
	if readyPlan.Checksum != v2.Checksum {
		t.Fatalf("a ready capability did not issue the v2 format: %q", readyPlan.Checksum)
	}

	receipt, err := service.RollbackCompanionIndexes(ctx)
	if err != nil {
		t.Fatalf("roll back companion index rollout: %v", err)
	}
	if receipt.State != CompanionIndexPending {
		t.Fatalf("rollback receipt = %#v", receipt)
	}

	rolledBack, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := compilePlan(catalog, change, false)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Checksum != v1.Checksum {
		t.Fatalf("rollback did not return issuance to v1: %q", rolledBack.Checksum)
	}
	// A downgrade never drops what it built, so a forward fix does not rebuild it.
	assertCompanionIndexShape(t, pool, companion)

	// And a create after the downgrade really is a v1 create: no companion. That
	// is what makes the rollback a downgrade rather than a relabelling.
	applyChange(t, service, change, administratorID)
	if diagnostics := readDiagnostics(t, pool); diagnostics.Expected != 2 || diagnostics.Missing != 1 {
		t.Fatalf("diagnostics after a v1 create = %#v", diagnostics)
	}

	// Re-activation still has to prove the fleet, which is what stops a writer
	// that predates the companion contract from being activated over. This half is
	// pre-existing behaviour, asserted here because the commitment point rests on
	// it: nothing is committed for the fleet until every live lease is at v2.
	leases, err := service.SchemaWriterFleet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) == 0 {
		t.Fatal("no schema writer lease to downgrade")
	}
	proof := SchemaWriterFleetProof{}
	for _, lease := range leases {
		proof.Instances = append(proof.Instances, lease.InstanceID)
	}
	if _, err := pool.Exec(ctx,
		"update pgbase_meta.schema_writer_leases set compiler_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateCompanionIndexes(ctx, proof); !errors.Is(err, ErrCompanionIndexFleet) {
		t.Fatalf("activation over a v1-only writer = %v, want ErrCompanionIndexFleet", err)
	}
}
