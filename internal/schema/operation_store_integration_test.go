//go:build integration

package schema

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var operationStoreTestCursorSecret = []byte("0123456789abcdef0123456789abcdef")

func TestClaimOperationCreatesOneGuardedRunningReceipt(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	service := NewService(pool, &Cache{}, nil, operationStoreTestCursorSecret)
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000601")
	intent, err := GuardedApply(
		NewCreateChange(CreateCollectionInput{Name: "claim_posts"}),
		Plan{BaseVersion: 0, Checksum: strings.Repeat("11", 32)},
		administratorID,
		operationID,
		"schema-operation-key-0601",
	)
	if err != nil {
		t.Fatalf("GuardedApply(): %v", err)
	}

	claim, err := service.claimOperation(context.Background(), intent)
	if err != nil {
		t.Fatalf("claimOperation(): %v", err)
	}
	if !claim.acquired || claim.replayed {
		t.Fatalf("claim disposition = acquired:%t replayed:%t, want fresh claim", claim.acquired, claim.replayed)
	}
	row := claim.row
	if row.operationID != operationID || row.actorID != administratorID ||
		row.resourceID == uuid.Nil || row.resourceID.Version() != 7 ||
		row.operation != SchemaOperationCollectionCreate || row.mode != operationModeGuarded ||
		row.status != OperationRunning || row.fenceGeneration != 1 ||
		row.leaseOwner == nil || *row.leaseOwner != claim.leaseOwner {
		t.Fatalf("fresh operation row = %#v, claim = %#v", row, claim)
	}
	if row.createdAt.Location() != time.UTC || !row.createdAt.Equal(row.updatedAt) ||
		row.leaseExpiresAt == nil || row.leaseExpiresAt.Sub(row.updatedAt) != 2*time.Minute ||
		row.reconcileAfter == nil || row.reconcileAfter.Sub(*row.leaseExpiresAt) != 10*time.Second {
		t.Fatalf("fresh operation timestamps = created:%s updated:%s lease:%v reconcile:%v", row.createdAt, row.updatedAt, row.leaseExpiresAt, row.reconcileAfter)
	}

	ref, err := NewOperationRef(administratorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.OperationStatus(context.Background(), ref)
	if err != nil {
		t.Fatalf("OperationStatus(): %v", err)
	}
	if receipt.OperationID != operationID || receipt.CollectionID != row.resourceID ||
		receipt.Status != OperationRunning || receipt.BaseVersion != 0 ||
		receipt.CreatedAt.Location() != time.UTC || receipt.UpdatedAt.Location() != time.UTC {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestClaimOperationReplaysLiveClaimsWithoutBlockingAndRejectsCollisions(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	service := NewService(pool, &Cache{}, nil, operationStoreTestCursorSecret)
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000611")
	intent := guardedCreateIntent(t, administratorID, operationID, "replay_posts", "schema-operation-key-0611")
	first, err := service.claimOperation(context.Background(), intent)
	if err != nil {
		t.Fatalf("first claimOperation(): %v", err)
	}

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	lockingTx, err := connection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lockingTx.Rollback(context.Background())
		}
	}()
	if _, err := lockingTx.Exec(context.Background(), `select operation_id
		from pgbase_meta.schema_operations where operation_id = $1 for update`, operationID); err != nil {
		t.Fatal(err)
	}

	replayContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	replayed, err := service.claimOperation(replayContext, intent)
	if err != nil {
		t.Fatalf("live replay waited for the operation row lock: %v", err)
	}
	if replayed.acquired || !replayed.replayed || replayed.row.resourceID != first.row.resourceID ||
		replayed.row.fenceGeneration != first.row.fenceGeneration {
		t.Fatalf("live replay = %#v, first = %#v", replayed, first)
	}
	if err := lockingTx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked = false

	differentRequest := guardedCreateIntent(t, administratorID, operationID, "other_posts", "schema-operation-key-0611")
	if _, err := service.claimOperation(context.Background(), differentRequest); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation ID with another request error = %v, want ErrOperationConflict", err)
	}
	differentOperation := guardedCreateIntent(t, administratorID,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000612"),
		"replay_posts", "schema-operation-key-0611")
	if _, err := service.Apply(context.Background(), differentOperation); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same idempotency key and request with another operation ID error = %v, want ErrOperationConflict", err)
	}
	ref, err := NewOperationRef(administratorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.OperationStatus(context.Background(), ref)
	if err != nil || receipt.OperationID != operationID || receipt.Status != OperationRunning {
		t.Fatalf("original running receipt after alternate ID = %+v, error = %v", receipt, err)
	}
	differentIdentityRequest, err := GuardedApply(
		NewCreateChange(CreateCollectionInput{
			Name: "replay_posts", Fields: []FieldInput{{Name: "title", Type: TypeText}},
		}),
		Plan{BaseVersion: 0, Checksum: strings.Repeat("11", 32)},
		administratorID,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000613"),
		"schema-operation-key-0611",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.claimOperation(context.Background(), differentIdentityRequest); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same idempotency identity with another request error = %v, want ErrOperationConflict", err)
	}
}

func TestClaimOperationFencesExpiredRunningAndEligibleUnknownClaims(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	service := NewService(pool, &Cache{}, nil, operationStoreTestCursorSecret)
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000621")
	intent := guardedCreateIntent(t, administratorID, operationID, "takeover_posts", "schema-operation-key-0621")
	first, err := service.claimOperation(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.schema_operations
		set created_at = current_timestamp - interval '3 minutes',
			updated_at = current_timestamp - interval '3 minutes',
			lease_expires_at = current_timestamp - interval '1 minute',
			reconcile_after = current_timestamp - interval '50 seconds'
		where operation_id = $1`, operationID); err != nil {
		t.Fatalf("expire running claim: %v", err)
	}

	second, err := service.claimOperation(context.Background(), intent)
	if err != nil {
		t.Fatalf("take over expired running claim: %v", err)
	}
	if !second.acquired || second.replayed || second.fenceGeneration != 2 ||
		second.row.resourceID != first.row.resourceID || second.leaseOwner == first.leaseOwner ||
		second.row.leaseExpiresAt == nil || second.row.leaseExpiresAt.Sub(second.row.updatedAt) != 2*time.Minute ||
		second.row.reconcileAfter == nil || second.row.reconcileAfter.Sub(*second.row.leaseExpiresAt) != 10*time.Second {
		t.Fatalf("expired-running takeover = %#v, first = %#v", second, first)
	}

	if _, err := pool.Exec(context.Background(), `update pgbase_meta.schema_operations
		set status = 'unknown', lease_owner = null, lease_expires_at = null,
			updated_at = current_timestamp - interval '20 seconds',
			reconcile_after = current_timestamp - interval '10 seconds'
		where operation_id = $1`, operationID); err != nil {
		t.Fatalf("make unknown claim eligible: %v", err)
	}
	third, err := service.claimOperation(context.Background(), intent)
	if err != nil {
		t.Fatalf("take over eligible unknown claim: %v", err)
	}
	if !third.acquired || third.replayed || third.fenceGeneration != 3 ||
		third.row.resourceID != first.row.resourceID || third.leaseOwner == second.leaseOwner {
		t.Fatalf("unknown takeover = %#v, second = %#v", third, second)
	}
}

func TestClaimAndTakeoverLeasesStartAfterTheirTransactionWait(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000622")
	intent := guardedCreateIntent(t, administratorID, operationID, "lease_clock_posts", "schema-operation-key-0622")
	identity, err := newOperationClaimIdentity(intent)
	if err != nil {
		t.Fatal(err)
	}
	resourceID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000623")
	firstLeaseOwner := uuid.MustParse("019551d0-e9c0-7000-8000-000000000624")

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		connection.Release()
		t.Fatal(err)
	}
	firstTransactionOpen := true
	defer func() {
		if firstTransactionOpen {
			_ = tx.Rollback(context.Background())
			connection.Release()
		}
	}()
	var transactionStarted time.Time
	if err := tx.QueryRow(ctx, "select transaction_timestamp()").Scan(&transactionStarted); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "select pg_sleep(0.05)"); err != nil {
		t.Fatal(err)
	}
	inserted, err := insertOperationClaim(ctx, tx, identity, resourceID, firstLeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if inserted == nil || !inserted.updatedAt.After(transactionStarted.Add(25*time.Millisecond)) ||
		inserted.leaseExpiresAt == nil || inserted.leaseExpiresAt.Sub(inserted.updatedAt) != 2*time.Minute ||
		inserted.reconcileAfter == nil || inserted.reconcileAfter.Sub(*inserted.leaseExpiresAt) != 10*time.Second {
		t.Fatalf("inserted lease used transaction-start time: started=%s row=%#v", transactionStarted, inserted)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	firstTransactionOpen = false
	connection.Release()

	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET created_at = current_timestamp - interval '3 minutes',
			updated_at = current_timestamp - interval '3 minutes',
			lease_expires_at = current_timestamp - interval '1 minute',
			reconcile_after = current_timestamp - interval '50 seconds'
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatal(err)
	}
	connection, err = pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err = connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, "select transaction_timestamp()").Scan(&transactionStarted); err != nil {
		t.Fatal(err)
	}
	found, err := findOperationClaim(ctx, tx, identity)
	if err != nil || found == nil {
		t.Fatalf("find expired claim = %#v, %v", found, err)
	}
	if _, err := tx.Exec(ctx, "select pg_sleep(0.05)"); err != nil {
		t.Fatal(err)
	}
	secondLeaseOwner := uuid.MustParse("019551d0-e9c0-7000-8000-000000000625")
	taken, err := takeOrReplayOperation(ctx, tx, identity, secondLeaseOwner, *found)
	if err != nil {
		t.Fatal(err)
	}
	if !taken.acquired || !taken.row.updatedAt.After(transactionStarted.Add(25*time.Millisecond)) ||
		taken.row.leaseExpiresAt == nil || taken.row.leaseExpiresAt.Sub(taken.row.updatedAt) != 2*time.Minute ||
		taken.row.reconcileAfter == nil || taken.row.reconcileAfter.Sub(*taken.row.leaseExpiresAt) != 10*time.Second {
		t.Fatalf("takeover lease used transaction-start time: started=%s claim=%#v", transactionStarted, taken)
	}
}

func TestOperationStatusAndHistoryAreOwnerScopedBoundedAndCursorStable(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	service := NewService(pool, &Cache{}, nil, operationStoreTestCursorSecret)
	otherAdministratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000639")
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		values ($1, 'operation-history-other@example.com', 'test', true, current_timestamp, current_timestamp)`,
		otherAdministratorID,
	); err != nil {
		t.Fatalf("insert other administrator: %v", err)
	}

	operationIDs := []uuid.UUID{
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000631"),
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000632"),
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000633"),
	}
	for index, operationID := range operationIDs {
		intent := guardedCreateIntent(t, administratorID, operationID,
			"history_posts_"+string(rune('a'+index)),
			"schema-operation-key-063"+string(rune('1'+index)))
		if _, err := service.claimOperation(context.Background(), intent); err != nil {
			t.Fatalf("claim operation %d: %v", index, err)
		}
	}
	tieTime := time.Date(2026, time.September, 2, 5, 6, 7, 123456000, time.UTC)
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.schema_operations
		set created_at = $2::timestamptz, updated_at = $2::timestamptz,
			lease_expires_at = $2::timestamptz + interval '2 minutes',
			reconcile_after = $2::timestamptz + interval '2 minutes 10 seconds'
		where actor_id = $1`, administratorID, tieTime); err != nil {
		t.Fatalf("make history timestamps equal: %v", err)
	}

	wrongOwnerRef, err := NewOperationRef(otherAdministratorID, operationIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	missingRef, err := NewOperationRef(administratorID,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000638"))
	if err != nil {
		t.Fatal(err)
	}
	_, wrongOwnerErr := service.OperationStatus(context.Background(), wrongOwnerRef)
	_, missingErr := service.OperationStatus(context.Background(), missingRef)
	if wrongOwnerErr != ErrOperationNotFound || missingErr != ErrOperationNotFound {
		t.Fatalf("wrong-owner/missing errors = %v / %v, want identical ErrOperationNotFound", wrongOwnerErr, missingErr)
	}

	issuedAt := tieTime.Add(time.Minute)
	now := issuedAt
	service.now = func() time.Time { return now }
	firstQuery, err := NewOperationQuery(administratorID, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ListOperations(context.Background(), firstQuery)
	if err != nil {
		t.Fatalf("ListOperations(first): %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].OperationID != operationIDs[2] ||
		first.Items[1].OperationID != operationIDs[1] || first.NextCursor == "" ||
		operationPageSize(first) > maximumOperationPageBytes {
		t.Fatalf("first history page = %#v (size %d)", first, operationPageSize(first))
	}
	secondQuery, err := NewOperationQuery(administratorID, 2, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListOperations(context.Background(), secondQuery)
	if err != nil {
		t.Fatalf("ListOperations(second): %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].OperationID != operationIDs[0] || second.NextCursor != "" {
		t.Fatalf("second history page = %#v", second)
	}

	otherQuery, err := NewOperationQuery(otherAdministratorID, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	otherPage, err := service.ListOperations(context.Background(), otherQuery)
	if err != nil || len(otherPage.Items) != 0 {
		t.Fatalf("other owner history = %#v, %v", otherPage, err)
	}
	otherCursorQuery, err := NewOperationQuery(otherAdministratorID, 2, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListOperations(context.Background(), otherCursorQuery); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("cross-owner cursor error = %v, want ErrInvalidOperation", err)
	}
	tampered := first.NextCursor[:len(first.NextCursor)/2] + "A" + first.NextCursor[len(first.NextCursor)/2+1:]
	tamperedQuery, err := NewOperationQuery(administratorID, 2, tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListOperations(context.Background(), tamperedQuery); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("tampered cursor error = %v, want ErrInvalidOperation", err)
	}
	now = issuedAt.Add(schemaOperationCursorTTL)
	if _, err := service.ListOperations(context.Background(), secondQuery); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("expired cursor error = %v, want ErrInvalidOperation", err)
	}
}

func guardedCreateIntent(
	t *testing.T,
	actorID uuid.UUID,
	operationID uuid.UUID,
	name string,
	idempotencyKey string,
) ApplyIntent {
	t.Helper()
	intent, err := GuardedApply(
		NewCreateChange(CreateCollectionInput{Name: name}),
		Plan{BaseVersion: 0, Checksum: strings.Repeat("11", 32)},
		actorID,
		operationID,
		idempotencyKey,
	)
	if err != nil {
		t.Fatalf("GuardedApply(): %v", err)
	}
	return intent
}
