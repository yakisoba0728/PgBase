//go:build integration

package schema

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
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

// The narrowing has to stop at an update. Adding a nullable indexed field to an
// existing collection can name a companion nothing has built, so it still closes
// the gate and still costs the approved CIC maintenance path. This is the test
// that fails if the demote is deleted instead of narrowed.
func TestUpdatingAnExistingCollectionStillClosesTheCompanionGate(t *testing.T) {
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

// D2-4 names create and update. A delete is neither, and it cannot leave a
// companion unbuilt because dropping the table drops every index on it, so it
// carries the capability rather than closing it. Carrying it means moving the
// promoted count too: doctor compares that count against the catalog.
func TestDeletingACollectionCarriesTheCompanionCapability(t *testing.T) {
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
	if _, err := service.Apply(ctx, LegacyApply(change,
		Plan{Checksum: v2.Checksum, BaseVersion: pendingCatalog.Version}, administratorID)); err != nil {
		t.Fatalf("a v1 writer refused a v2 checksum: %v", err)
	}

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
	// The writer executes its own plan, so the companion is built even though the
	// caller only ever agreed to the v1 statement list.
	assertCompanionIndexShape(t, pool, companionSpec(t, service, result.Collection, "nickname"))
	if status := companionStatus(t, service); !status.Ready() {
		t.Fatalf("capability after a dual-validated create = %#v", status)
	}
}

// The downgrade half. Rollback is the commitment point in reverse: it returns
// issuance to v1 for every later plan, keeps every index already built, and
// leaves re-activation to prove the fleet again.
func TestCompanionRollbackReturnsPlanIssuanceToV1AndKeepsTheIndex(t *testing.T) {
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
