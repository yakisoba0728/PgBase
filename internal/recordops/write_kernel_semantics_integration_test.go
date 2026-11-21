//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// guardedCreateRules lets a user create a record only for themselves
// (author = @request.auth.id) and only with a small count.
func guardedCreateRules() accessrule.Rules {
	owner := accessrule.Rule{Op: "owner"}
	create := accessrule.Rule{Op: "all", Args: []accessrule.Rule{
		{Op: "eq", Field: "author", Auth: "id"},
		{Op: "lt", Field: "count", Value: json.RawMessage(`10`)},
	}}
	return accessrule.Rules{List: owner, View: owner, Create: create, Update: owner, Delete: owner}
}

// newWriteSemanticsFixture is newUserAccessFixture that also returns the
// database URL, for dedicated traced connections, and injects a registered
// membership runtime so a borrowed transaction can write first.
func newWriteSemanticsFixture(t *testing.T) (string, userAccessFixture) {
	t.Helper()
	databaseURL, pool, schemas, _, admin := newRecordOperationsTestWithURL(t)
	runtime, err := membership.RegisterWriter(context.Background(), membership.New(pool), membership.WriterC)
	if err != nil {
		t.Fatal(err)
	}
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime)
	users := []Principal{{Kind: PrincipalUser, ID: uuid.New()}, {Kind: PrincipalUser, ID: uuid.New()}}
	for index, user := range users {
		if _, err := pool.Exec(context.Background(), `INSERT INTO pgbase_meta.users(id,email,password_hash,active,created_at,updated_at)
			VALUES($1,$2,'test',true,transaction_timestamp(),transaction_timestamp())`, user.ID, fmt.Sprintf("semantics%d@example.test", index)); err != nil {
			t.Fatal(err)
		}
	}
	return databaseURL, userAccessFixture{pool: pool, schemas: schemas, records: records, admin: admin, first: users[0], second: users[1]}
}

// newGuardedCreateFixture adds a relation-free collection and one with a
// relation field, both under guardedCreateRules and open to users.
func newGuardedCreateFixture(t *testing.T) (string, userAccessFixture, map[string]string) {
	t.Helper()
	databaseURL, f := newWriteSemanticsFixture(t)
	ctx := context.Background()
	apply := func(input schema.CreateCollectionInput) {
		t.Helper()
		plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(input))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, f.admin.ID)); err != nil {
			t.Fatal(err)
		}
	}
	guardedFields := func() []schema.FieldInput {
		return []schema.FieldInput{
			{Name: "label", Type: schema.TypeText, Required: true},
			{Name: "author", Type: schema.TypeUUID},
			{Name: "count", Type: schema.TypeInteger},
		}
	}
	apply(schema.CreateCollectionInput{Name: "guarded", Fields: guardedFields()})
	apply(schema.CreateCollectionInput{Name: "guarded_links", Fields: append(guardedFields(), schema.FieldInput{
		Name: "target", Type: schema.TypeRelation,
		Relation: &schema.RelationInput{TargetCollection: "guarded", Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull},
	})})
	rules := recordaccess.NewRuleService(f.pool, f.schemas)
	for _, name := range []string{"guarded", "guarded_links"} {
		f.enable(t, name)
		if _, err := rules.Set(ctx, name, guardedCreateRules()); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := f.schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]string{}
	for _, name := range []string{"guarded", "guarded_links"} {
		collection := catalog.Collections[name]
		if name == "guarded" && hasRelationFields(collection) {
			t.Fatal("guarded unexpectedly has relation fields")
		}
		if name == "guarded_links" && !hasRelationFields(collection) {
			t.Fatal("guarded_links has no relation field")
		}
		tables[name] = collection.TableName
	}
	return databaseURL, f, tables
}

func guardedInput(label string, author uuid.UUID, count int) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"label":  json.RawMessage(fmt.Sprintf("%q", label)),
		"author": json.RawMessage(fmt.Sprintf("%q", author)),
		"count":  json.RawMessage(fmt.Sprint(count)),
	}
}

// TestCreateRuleIsEnforcedOnEveryCreatePath pins that a create the rule
// denies leaves no trace, on the owned Repeatable Read path that skips the
// second create-rule check (no idempotency key, no relation field) as well as
// on every path that keeps it. The first check after the insert is the one
// that must deny; the insert is rolled back with the transaction or savepoint.
func TestCreateRuleIsEnforcedOnEveryCreatePath(t *testing.T) {
	_, f, tables := newGuardedCreateFixture(t)
	ctx := context.Background()
	user, other := f.first, f.second

	rowCount := func(table, label string) int {
		t.Helper()
		var count int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{"app", table}.Sanitize()+` WHERE label=$1`, label).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	artifactCount := func() (owners, audits int) {
		t.Helper()
		if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.record_owners WHERE user_id=$1),
			(SELECT count(*) FROM pgbase_meta.record_audit_events WHERE actor_id=$1)`, user.ID).Scan(&owners, &audits); err != nil {
			t.Fatal(err)
		}
		return owners, audits
	}

	for _, collection := range []string{"guarded", "guarded_links"} {
		for _, keyed := range []bool{false, true} {
			for _, borrowed := range []bool{false, true} {
				for _, input := range []struct {
					name    string
					author  uuid.UUID
					count   int
					allowed bool
				}{
					{"allowed", user.ID, 3, true},
					{"other author", other.ID, 3, false},
					{"count too high", user.ID, 50, false},
				} {
					name := fmt.Sprintf("%s/keyed=%t/borrowed=%t/%s", collection, keyed, borrowed, input.name)
					t.Run(name, func(t *testing.T) {
						label := "guarded-" + uuid.NewString()
						request := CreateRequest{
							Principal: user, RequestID: "guarded-" + uuid.NewString(), Collection: collection,
							Input: guardedInput(label, input.author, input.count),
						}
						if keyed {
							request.IdempotencyKey = "guarded-key-" + uuid.NewString()
						}
						ownersBefore, auditsBefore := artifactCount()
						var err error
						if borrowed {
							tx, beginErr := f.pool.Begin(ctx)
							if beginErr != nil {
								t.Fatal(beginErr)
							}
							_, err = f.records.Create(WithTransaction(ctx, tx), request)
							// The owner commits regardless; a denied operation's
							// savepoint must already be rolled back.
							if commitErr := tx.Commit(ctx); commitErr != nil {
								t.Fatal(commitErr)
							}
						} else {
							_, err = f.records.Create(ctx, request)
						}
						ownersAfter, auditsAfter := artifactCount()
						if input.allowed {
							if err != nil {
								t.Fatalf("allowed create = %v", err)
							}
							if got := rowCount(tables[collection], label); got != 1 {
								t.Fatalf("allowed create stored %d rows, want 1", got)
							}
							if ownersAfter != ownersBefore+1 || auditsAfter != auditsBefore+1 {
								t.Fatalf("allowed create artifacts owners %d->%d audits %d->%d", ownersBefore, ownersAfter, auditsBefore, auditsAfter)
							}
							return
						}
						if !errors.Is(err, recordaccess.ErrDenied) {
							t.Fatalf("denied create = %v, want %v", err, recordaccess.ErrDenied)
						}
						if got := rowCount(tables[collection], label); got != 0 {
							t.Fatalf("denied create left %d rows", got)
						}
						if ownersAfter != ownersBefore || auditsAfter != auditsBefore {
							t.Fatalf("denied create left artifacts owners %d->%d audits %d->%d", ownersBefore, ownersAfter, auditsBefore, auditsAfter)
						}
					})
				}
			}
		}
	}
}

// TestBorrowedCreateKeepsSecondCreateRuleCheck pins, on a traced borrowed
// transaction, that a create still evaluates its create rule twice (the
// EXISTS check after the insert and the set-based count in commitMutation)
// with one memoized rule load, whether or not the create carries an
// idempotency key or touches a collection with relation fields. Only an owned
// Repeatable Read create on a relation-free collection without a key may skip
// the second evaluation; an owned transaction cannot be traced here because
// database.Open accepts no tracer.
func TestBorrowedCreateKeepsSecondCreateRuleCheck(t *testing.T) {
	databaseURL, f, tables := newGuardedCreateFixture(t)
	ctx := context.Background()

	trace := &writeKernelTrace{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = trace
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })

	run := func(request CreateRequest) writeKernelMeasurement {
		t.Helper()
		tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		trace.start()
		_, operationErr := f.records.Create(WithTransaction(ctx, tx), request)
		measured := trace.stop()
		if operationErr != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("create %s: %v\n%s", request.Collection, operationErr, measured)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return measured
	}
	request := func(collection string, keyed bool) CreateRequest {
		result := CreateRequest{
			Principal: f.first, RequestID: "second-check-" + uuid.NewString(), Collection: collection,
			Input: guardedInput("second-check-"+uuid.NewString(), f.first.ID, 1),
		}
		if keyed {
			result.IdempotencyKey = "second-check-key-" + uuid.NewString()
		}
		return result
	}
	// Warm the connection and the lazily registered membership writer.
	run(request("guarded", false))

	for _, test := range []struct {
		collection string
		keyed      bool
	}{{"guarded", false}, {"guarded", true}, {"guarded_links", false}, {"guarded_links", true}} {
		measured := run(request(test.collection, test.keyed))
		table := pgx.Identifier{"app", tables[test.collection]}.Sanitize()
		first := fmt.Sprintf("select exists(select 1 from %s where id=$1 and", table)
		second := fmt.Sprintf("select count(*) from %s where id=any($1::uuid[]) and", table)
		name := fmt.Sprintf("%s keyed=%t", test.collection, test.keyed)
		if got := measured.count(traceLoadRules); got != 1 {
			t.Errorf("%s rule loads = %d, want 1\n%s", name, got, measured)
		}
		if got := measured.count(first); got != 1 {
			t.Errorf("%s first create-rule checks = %d, want 1\n%s", name, got, measured)
		}
		if got := measured.count(second); got != 1 {
			t.Errorf("%s second create-rule checks = %d, want 1\n%s", name, got, measured)
		}
	}
}

// pausingHook stops the first armed mutation inside commitMutation, after
// every admission and rule check of the attempt, and reports the backend PID
// of the transaction that holds those checks' locks.
type pausingHook struct {
	mu          sync.Mutex
	armed       bool
	reached     chan int
	release     chan struct{}
	releaseOnce sync.Once
}

// newPausingHook releases a still-paused mutation when the test ends, so a
// failed assertion does not leave it waiting for its own timeout.
func newPausingHook(t *testing.T) *pausingHook {
	hook := &pausingHook{reached: make(chan int, 1), release: make(chan struct{})}
	t.Cleanup(hook.resume)
	return hook
}

func (h *pausingHook) resume() { h.releaseOnce.Do(func() { close(h.release) }) }

func (h *pausingHook) arm() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.armed = true
}

func (h *pausingHook) BeforeMutation(ctx context.Context, _ Mutation) error {
	h.mu.Lock()
	armed := h.armed
	h.armed = false
	h.mu.Unlock()
	if !armed {
		return nil
	}
	policies, _ := ctx.Value(writePoliciesKey{}).(*writePolicies)
	if policies == nil {
		return errors.New("paused mutation has no write policy memo")
	}
	var pid int
	if err := policies.tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		return err
	}
	h.reached <- pid
	select {
	case <-h.release:
		return nil
	case <-time.After(20 * time.Second):
		return errors.New("paused mutation was never released")
	}
}

func (*pausingHook) AfterMutation(context.Context, Mutation) {}

// waitBlockedBy waits until some backend of this database waits on blocker
// and returns that backend's current statement. It fails if done delivers
// first: the concurrent writer finished without waiting.
func waitBlockedBy(t *testing.T, f userAccessFixture, blocker int, done <-chan error) string {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var statement string
		err := f.pool.QueryRow(ctx, `SELECT query FROM pg_stat_activity
			WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) LIMIT 1`, blocker).Scan(&statement)
		if err == nil {
			return statement
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("concurrent writer finished without waiting for the paused mutation: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent writer never waited on the paused mutation")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestReusedWriteLocksHoldOffPolicyAndUserWriters pins the concurrency claims
// the write policy memo relies on. While a user's update or delete is paused
// after its last reused admission/rule check, a RuleService write of the
// collection's first rules row and a deactivation of that user both wait for
// the mutation's transaction to end.
func TestReusedWriteLocksHoldOffPolicyAndUserWriters(t *testing.T) {
	for _, operation := range []string{"update", "delete"} {
		for _, writer := range []string{"first rules row", "user deactivation"} {
			t.Run(operation+"/"+writer, func(t *testing.T) {
				f := newUserAccessFixture(t)
				ctx := context.Background()
				f.enable(t, "records")
				catalog, err := f.schemas.Catalog(ctx)
				if err != nil {
					t.Fatal(err)
				}
				collectionID := catalog.Collections["records"].ID
				// Start from the implicit owner policy: no rules row exists, so
				// the memo may remember absent rules only because it holds the
				// collection grant.
				if _, err := f.pool.Exec(ctx, `DELETE FROM pgbase_meta.collection_rules WHERE collection_id=$1`, collectionID); err != nil {
					t.Fatal(err)
				}
				created := f.create(t, f.first, "paused-"+operation, "")
				hook := newPausingHook(t)
				f.records.SetHooks([]MutationHook{hook})
				hook.arm()

				mutated := make(chan error, 1)
				go func() {
					var err error
					if operation == "update" {
						_, err = f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "paused-update-" + uuid.NewString(), Collection: "records", ID: created.InternalID,
							Input: map[string]json.RawMessage{"title": json.RawMessage(`"paused-updated"`)}})
					} else {
						_, err = f.records.Delete(ctx, DeleteRequest{Principal: f.first, RequestID: "paused-delete-" + uuid.NewString(), Collection: "records", ID: created.InternalID})
					}
					mutated <- err
				}()
				var blocker int
				select {
				case blocker = <-hook.reached:
				case err := <-mutated:
					t.Fatalf("%s finished before pausing: %v", operation, err)
				case <-time.After(10 * time.Second):
					t.Fatalf("%s never reached the pause", operation)
				}

				written := make(chan error, 1)
				go func() {
					if writer == "first rules row" {
						_, err := recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", accessrule.OwnerRules())
						written <- err
						return
					}
					_, err := f.pool.Exec(ctx, `UPDATE pgbase_meta.users SET active=false WHERE id=$1`, f.first.ID)
					written <- err
				}()
				statement := strings.Join(strings.Fields(waitBlockedBy(t, f, blocker, written)), " ")
				// The memo's claim names the lock each writer waits on: the rule
				// writer's collection_access FOR UPDATE against the grant's FOR
				// SHARE, and the users update against the users FOR SHARE.
				want := "pgbase_meta.collection_access"
				if writer == "user deactivation" {
					want = "UPDATE pgbase_meta.users"
				}
				if !strings.Contains(statement, want) {
					t.Fatalf("%s waits on the paused %s at %q, want a statement on %s", writer, operation, statement, want)
				}

				hook.resume()
				select {
				case err := <-mutated:
					if err != nil {
						t.Fatalf("paused %s = %v", operation, err)
					}
				case <-time.After(10 * time.Second):
					t.Fatalf("paused %s did not finish", operation)
				}
				select {
				case err := <-written:
					if err != nil {
						t.Fatalf("%s after the mutation committed = %v", writer, err)
					}
				case <-time.After(10 * time.Second):
					t.Fatalf("%s did not finish after the mutation committed", writer)
				}

				if writer == "first rules row" {
					var revision int64
					if err := f.pool.QueryRow(ctx, `SELECT revision FROM pgbase_meta.collection_rules WHERE collection_id=$1`, collectionID).Scan(&revision); err != nil {
						t.Fatalf("first rules row was not written: %v", err)
					}
					return
				}
				_, err = f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "deactivated-" + uuid.NewString(), Collection: "records",
					Input: map[string]json.RawMessage{"title": json.RawMessage(`"after-deactivation"`)}})
				if !errors.Is(err, recordaccess.ErrDenied) {
					t.Fatalf("create by deactivated user = %v, want %v", err, recordaccess.ErrDenied)
				}
			})
		}
	}
}

// TestSavepointRollbackDropsReusedAdmission pins that the memo never outlives
// the savepoint that took its locks: after an operation in a borrowed
// transaction fails and its savepoint rolls back (releasing the row locks
// AuthorizeUser took inside it), the next operation in the same transaction
// runs AuthorizeUser again and holds the users row lock afresh.
func TestSavepointRollbackDropsReusedAdmission(t *testing.T) {
	databaseURL, f := newWriteSemanticsFixture(t)
	ctx := context.Background()
	f.enable(t, "records")
	created := f.create(t, f.first, "savepoint-admission", "")

	trace := &writeKernelTrace{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = trace
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })

	userRowLocked := func() bool {
		t.Helper()
		probe, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = probe.Rollback(ctx) }()
		_, err = probe.Exec(ctx, `SELECT 1 FROM pgbase_meta.users WHERE id=$1 FOR UPDATE NOWAIT`, f.first.ID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		return false
	}

	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	borrowed := WithTransaction(ctx, tx)
	update := func(ifMatch EntityTagSet, title string) (writeKernelMeasurement, error) {
		trace.start()
		_, err := f.records.Update(borrowed, UpdateRequest{Principal: f.first, RequestID: "savepoint-" + uuid.NewString(), Collection: "records", ID: created.InternalID,
			IfMatch: ifMatch, Input: map[string]json.RawMessage{"title": json.RawMessage(fmt.Sprintf("%q", title))}})
		return trace.stop(), err
	}

	failed, err := update(EntityTagSet{Present: true, Values: []string{`"pgb1.stale"`}}, "savepoint-stale")
	if !IsCode(err, CodePreconditionFailed) {
		t.Fatalf("stale update = %v, want %s", err, CodePreconditionFailed)
	}
	if got := failed.count(traceAuthorizeUser); got != 1 {
		t.Fatalf("failed update admission checks = %d, want 1\n%s", got, failed)
	}
	if userRowLocked() {
		t.Fatal("users row stayed locked after the failed operation's savepoint rolled back")
	}

	succeeded, err := update(EntityTagSet{}, "savepoint-updated")
	if err != nil {
		t.Fatal(err)
	}
	if got := succeeded.count(traceAuthorizeUser); got != 1 {
		t.Fatalf("update after a rolled-back savepoint admission checks = %d, want 1\n%s", got, succeeded)
	}
	if !userRowLocked() {
		t.Fatal("users row is not locked by the operation after the rolled-back savepoint")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := f.records.Get(ctx, GetRequest{Principal: f.admin, RequestID: "savepoint-read-" + uuid.NewString(), Collection: "records", ID: created.InternalID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Data["title"] != "savepoint-updated" {
		t.Fatalf("stored title = %v", stored.Data["title"])
	}
}
