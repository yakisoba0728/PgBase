package schema

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGuardedApplySealsCanonicalOperationIdentity(t *testing.T) {
	actorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000501")
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000502")
	plan := Plan{BaseVersion: 7, Checksum: strings.Repeat("ab", 32)}
	first, err := GuardedApply(NewCreateChange(CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "metadata", Type: TypeJSONB, Default: []byte(` { "b": 2, "a": 1 } `)}},
	}), plan, actorID, operationID, "schema-operation-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	second, err := GuardedApply(NewCreateChange(CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "metadata", Type: TypeJSONB, Default: []byte(`{"a":1,"b":2}`)}},
	}), plan, actorID, operationID, "schema-operation-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if first.mode != operationModeGuarded || first.actor != actorID || first.operationID != operationID {
		t.Fatalf("guarded identity = %#v", first)
	}
	if first.baseVersion != plan.BaseVersion || first.checksum != plan.Checksum || first.planChecksum == ([32]byte{}) {
		t.Fatalf("guarded plan identity = %#v", first)
	}
	if first.resourceKeyHash == ([32]byte{}) || first.idempotencyKeyHash == ([32]byte{}) || first.requestHash == ([32]byte{}) {
		t.Fatalf("guarded hashes were not populated: %#v", first)
	}
	if first.resourceKeyHash != second.resourceKeyHash || first.idempotencyKeyHash != second.idempotencyKeyHash || first.requestHash != second.requestHash {
		t.Fatalf("canonical equivalents produced different identities: first=%#v second=%#v", first, second)
	}

	updated, err := GuardedApply(NewUpdateChange(UpdateCollectionInput{Name: "posts"}), plan, actorID,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000503"), "schema-operation-key-0002")
	if err != nil {
		t.Fatal(err)
	}
	if updated.resourceKeyHash != first.resourceKeyHash {
		t.Fatal("one collection key produced operation-dependent resource hashes")
	}
	if updated.requestHash == first.requestHash {
		t.Fatal("different schema operations produced one request hash")
	}
}

func TestGuardedApplyDoesNotRetainRawIdentityInItsSealedIntent(t *testing.T) {
	intent, err := GuardedApply(NewDeleteChange("private_collection"), Plan{
		BaseVersion: 3, Checksum: strings.Repeat("22", 32),
	}, uuid.MustParse("019551d0-e9c0-7000-8000-000000000651"),
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000652"), "raw-key-must-not-survive")
	if err != nil {
		t.Fatal(err)
	}
	if intent.idempotencyKeyHash == ([32]byte{}) {
		t.Fatal("guarded intent omitted the key hash")
	}
}

func TestGuardedApplyRejectsUnboundedOrUntrustedIdentity(t *testing.T) {
	actorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000511")
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000512")
	validChange := NewCreateChange(CreateCollectionInput{Name: "posts"})
	validPlan := Plan{BaseVersion: 0, Checksum: strings.Repeat("11", 32)}
	tests := []struct {
		name      string
		change    Change
		plan      Plan
		actor     uuid.UUID
		operation uuid.UUID
		key       string
	}{
		{name: "missing actor", change: validChange, plan: validPlan, operation: operationID, key: "schema-operation-key-0001"},
		{name: "missing operation", change: validChange, plan: validPlan, actor: actorID, key: "schema-operation-key-0001"},
		{name: "negative base", change: validChange, plan: Plan{BaseVersion: -1, Checksum: validPlan.Checksum}, actor: actorID, operation: operationID, key: "schema-operation-key-0001"},
		{name: "short checksum", change: validChange, plan: Plan{Checksum: "11"}, actor: actorID, operation: operationID, key: "schema-operation-key-0001"},
		{name: "non hex checksum", change: validChange, plan: Plan{Checksum: strings.Repeat("zz", 32)}, actor: actorID, operation: operationID, key: "schema-operation-key-0001"},
		{name: "short key", change: validChange, plan: validPlan, actor: actorID, operation: operationID, key: strings.Repeat("a", 15)},
		{name: "long key", change: validChange, plan: validPlan, actor: actorID, operation: operationID, key: strings.Repeat("a", 129)},
		{name: "unsafe key", change: validChange, plan: validPlan, actor: actorID, operation: operationID, key: "schema operation key"},
		{name: "invalid change", plan: validPlan, actor: actorID, operation: operationID, key: "schema-operation-key-0001"},
		{name: "invalid JSON", change: NewCreateChange(CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "data", Type: TypeJSONB, Default: []byte(`{"n":01}`)}}}), plan: validPlan, actor: actorID, operation: operationID, key: "schema-operation-key-0001"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := GuardedApply(testCase.change, testCase.plan, testCase.actor, testCase.operation, testCase.key); !errors.Is(err, ErrInvalidOperation) {
				t.Fatalf("GuardedApply() error = %v, want ErrInvalidOperation", err)
			}
		})
	}
}

func TestOperationReferenceAndQueryAreOwnerScopedAndBounded(t *testing.T) {
	actorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000521")
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000522")
	ref, err := NewOperationRef(actorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.actorID != actorID || ref.operationID != operationID {
		t.Fatalf("operation ref = %#v", ref)
	}
	for _, invalid := range [][2]uuid.UUID{{uuid.Nil, operationID}, {actorID, uuid.Nil}} {
		if _, err := NewOperationRef(invalid[0], invalid[1]); !errors.Is(err, ErrInvalidOperation) {
			t.Fatalf("NewOperationRef(%s, %s) error = %v", invalid[0], invalid[1], err)
		}
	}

	for _, testCase := range []struct {
		limit int
		want  int
	}{{limit: 0, want: 20}, {limit: 1, want: 1}, {limit: 100, want: 100}} {
		query, err := NewOperationQuery(actorID, testCase.limit, "")
		if err != nil {
			t.Fatal(err)
		}
		if query.actorID != actorID || query.limit != testCase.want || query.cursor != "" {
			t.Fatalf("operation query = %#v", query)
		}
	}
	for _, testCase := range []struct {
		actor  uuid.UUID
		limit  int
		cursor string
	}{
		{limit: 20},
		{actor: actorID, limit: -1},
		{actor: actorID, limit: 101},
		{actor: actorID, limit: 20, cursor: strings.Repeat("x", maximumOperationCursorEncodedBytes+1)},
	} {
		if _, err := NewOperationQuery(testCase.actor, testCase.limit, testCase.cursor); !errors.Is(err, ErrInvalidOperation) {
			t.Fatalf("NewOperationQuery(%s, %d) error = %v", testCase.actor, testCase.limit, err)
		}
	}
}

func TestOperationVocabularyMatchesTheDurableLedger(t *testing.T) {
	if SchemaOperationCollectionCreate != "collection_create" || SchemaOperationCollectionUpdate != "collection_update" || SchemaOperationCollectionDelete != "collection_delete" {
		t.Fatalf("schema operation vocabulary = %q/%q/%q", SchemaOperationCollectionCreate, SchemaOperationCollectionUpdate, SchemaOperationCollectionDelete)
	}
	if OperationRunning != "running" || OperationUnknown != "unknown" || OperationCommitted != "committed" || OperationFailed != "failed" {
		t.Fatalf("operation status vocabulary = %q/%q/%q/%q", OperationRunning, OperationUnknown, OperationCommitted, OperationFailed)
	}
	if OperationFailurePlanConflict != "plan_conflict" || OperationFailureApplyFailed != "apply_failed" || OperationFailureLeaseLost != "lease_lost" || OperationFailureReconcileFailed != "reconcile_failed" {
		t.Fatalf("operation failure vocabulary = %q/%q/%q/%q", OperationFailurePlanConflict, OperationFailureApplyFailed, OperationFailureLeaseLost, OperationFailureReconcileFailed)
	}
}

func TestOperationReadsFailClosedWhenTheServiceIsUnavailable(t *testing.T) {
	actorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000541")
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000542")
	ref, err := NewOperationRef(actorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	query, err := NewOperationQuery(actorID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	var service *Service
	if _, err := service.OperationStatus(context.Background(), ref); err == nil {
		t.Fatal("OperationStatus on an unavailable service succeeded")
	}
	if _, err := service.ListOperations(context.Background(), query); err == nil {
		t.Fatal("ListOperations on an unavailable service succeeded")
	}
}

func TestOperationRecoveryErrorCarriesOnlyASealedRecoveryReference(t *testing.T) {
	actorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000531")
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000532")
	ref, err := NewOperationRef(actorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	receipt := OperationReceipt{OperationID: operationID, Status: OperationUnknown}
	recovery := newOperationRecoveryError(ref, &receipt)
	if !errors.Is(recovery, ErrOperationOutcomeUnknown) || errors.Is(recovery, ErrOperationInProgress) {
		t.Fatalf("unknown recovery error = %v", recovery)
	}
	if recovery.Ref().ID() != operationID {
		t.Fatalf("recovery ref ID = %s", recovery.Ref().ID())
	}
	got, ok := recovery.Receipt()
	if !ok || got != receipt {
		t.Fatalf("recovery receipt = %#v, %t", got, ok)
	}

	running := receipt
	running.Status = OperationRunning
	if err := newOperationRecoveryError(ref, &running); !errors.Is(err, ErrOperationInProgress) || errors.Is(err, ErrOperationOutcomeUnknown) {
		t.Fatalf("running recovery error = %v", err)
	}
	withoutReceipt := newOperationRecoveryError(ref, nil)
	if _, ok := withoutReceipt.Receipt(); ok {
		t.Fatal("recovery error invented an unavailable receipt")
	}
}
